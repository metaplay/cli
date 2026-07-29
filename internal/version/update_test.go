/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"io/fs"
	"os"
	"path/filepath"
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

func TestCheckReplaceableAcceptsRunningExecutable(t *testing.T) {
	// Guards against the replaceability probe reporting a false negative for the executable
	// of the running process, which is locked against writes on Windows but still renamable.
	// Only meaningful on Windows, where checkReplaceable actually probes.
	exe, err := os.Executable()
	require.NoError(t, err)

	assert.NoError(t, checkReplaceable(exe))
}

func TestEnsureReplaceableRemovesStaleUpdateFiles(t *testing.T) {
	dir := t.TempDir()
	exe := writeExe(t, dir)

	stale := []string{
		filepath.Join(dir, ".metaplay.exe.new"),
		filepath.Join(dir, ".metaplay.exe.old"),
	}
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

	assert.Error(t, EnsureReplaceable(exe))
}

func TestRemoveStaleUpdateFilesReportsLockedLeftoverAsNonPermission(t *testing.T) {
	// A leftover held open by another process must not be classified as a permission problem,
	// or the caller renders an elevation hint that cannot help.
	dir := t.TempDir()
	exe := writeExe(t, dir)
	stale := filepath.Join(dir, ".metaplay.exe.old")
	require.NoError(t, os.WriteFile(stale, []byte("leftover"), 0o644))

	// Holding our own handle is enough on Windows; on Unix an open file is still unlinkable,
	// so there is nothing to assert there.
	release, locked := lockFile(t, stale)
	if !locked {
		t.Skip("open files do not block removal on this platform")
	}
	defer release()

	err := removeStaleUpdateFiles(exe)
	require.Error(t, err)
	assert.NotErrorIs(t, err, fs.ErrPermission)
}

func TestRemoveStaleUpdateFilesIgnoresMissingFiles(t *testing.T) {
	assert.NoError(t, removeStaleUpdateFiles(filepath.Join(t.TempDir(), "metaplay.exe")))
}
