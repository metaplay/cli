/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeExe creates a dummy executable in dir and returns its path.
func writeExe(t *testing.T, dir string) string {
	t.Helper()
	exe := filepath.Join(dir, "metaplay.exe")
	require.NoError(t, os.WriteFile(exe, []byte("binary"), 0o755))
	return exe
}

// assertBlockedLeftover checks the contract both platforms must satisfy for a leftover that
// cannot be removed. Getting this wrong is what made the CLI tell users to re-run elevated for
// a file that no privilege level can delete, so it is asserted from both platform tests.
func assertBlockedLeftover(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLeftoverInUse, "a blocked leftover must be reported as in-use")
	assert.NotErrorIs(t, err, fs.ErrPermission,
		"a blocked leftover must not look like an unwritable install, or the hint tells the user to elevate")
}

func TestUpdateTempPathsMatchTheLibraryNaming(t *testing.T) {
	// update.Apply builds its temp files as filepath.Join(Dir(target), "."+Base(target)+suffix).
	// If these drift apart the cleanup silently stops matching anything, so pin the shape —
	// including the extended-length form that GetExecutablePath returns on Windows.
	tests := []struct {
		name     string
		exe      string
		expected []string
	}{
		{
			name: "plain path",
			exe:  filepath.Join("install", "metaplay.exe"),
			expected: []string{
				filepath.Join("install", ".metaplay.exe.new"),
				filepath.Join("install", ".metaplay.exe.old"),
			},
		},
		{
			name: "no extension",
			exe:  filepath.Join("bin", "metaplay"),
			expected: []string{
				filepath.Join("bin", ".metaplay.new"),
				filepath.Join("bin", ".metaplay.old"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, updateTempPaths(tt.exe))
		})
	}

	// The library uses Dir+Base where this uses Split; they must agree on every input shape.
	for _, exe := range []string{
		filepath.Join("install", "metaplay.exe"),
		`\\?\C:\ProgramData\chocolatey\lib\metaplay\tools\metaplay.exe`,
		`\\?\UNC\server\share\tools\metaplay.exe`,
		"/usr/local/bin/metaplay",
		"metaplay.exe",
	} {
		dir, base := filepath.Dir(exe), filepath.Base(exe)
		assert.Equal(t,
			filepath.Join(dir, "."+base+".new"),
			updateTempPaths(exe)[0],
			"Split-based and Dir/Base-based construction must agree for %q", exe)
	}
}

func TestEnsureReplaceableAcceptsWritableInstall(t *testing.T) {
	dir := t.TempDir()
	exe := writeExe(t, dir)

	require.NoError(t, EnsureReplaceable(exe))

	// The probe file must not be left behind.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "metaplay.exe", entries[0].Name())
}

func TestEnsureReplaceableRemovesStaleUpdateFiles(t *testing.T) {
	dir := t.TempDir()
	exe := writeExe(t, dir)

	stale := updateTempPaths(exe)
	require.Len(t, stale, 2)
	for _, path := range stale {
		require.NoError(t, os.WriteFile(path, []byte("leftover"), 0o644))
	}

	require.NoError(t, EnsureReplaceable(exe))

	for _, path := range stale {
		assert.NoFileExists(t, path, "leftover from an earlier update should be removed")
	}
}

func TestEnsureReplaceableRejectsMissingDirectory(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "does-not-exist", "metaplay.exe")

	err := EnsureReplaceable(exe)

	require.Error(t, err)
	// Must not be mistaken for a busy leftover: that would suggest closing other processes.
	assert.NotErrorIs(t, err, ErrLeftoverInUse)
}

func TestRemoveStaleUpdateFilesIgnoresMissingFiles(t *testing.T) {
	assert.NoError(t, removeStaleUpdateFiles(filepath.Join(t.TempDir(), "metaplay.exe")))
}

func TestRemoveStaleUpdateFilesDoesNotRepeatThePath(t *testing.T) {
	// The message names the path once, via ForDisplay. Wrapping the whole *fs.PathError would
	// print it a second time, prefixed with \\?\ on Windows.
	dir := t.TempDir()
	exe := writeExe(t, dir)
	leftover := updateTempPaths(exe)[1]
	require.NoError(t, os.WriteFile(leftover, []byte("leftover"), 0o644))

	restore, blocked := blockDeletion(t, dir, exe, leftover)
	if !blocked {
		t.Skip("cannot make a file undeletable in this environment")
	}
	defer restore()

	err := removeStaleUpdateFiles(exe)
	require.Error(t, err)
	assertBlockedLeftover(t, err)
	assert.Equal(t, 1, strings.Count(err.Error(), filepath.Base(leftover)),
		"the leftover path should appear exactly once in %q", err.Error())
}
