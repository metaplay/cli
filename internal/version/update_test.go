/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
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

func TestCheckWritableAcceptsWritableInstall(t *testing.T) {
	dir := t.TempDir()
	exe := writeExe(t, dir)

	require.NoError(t, CheckWritable(exe))

	// The probe file must not be left behind.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "metaplay.exe", entries[0].Name())
}

func TestCheckWritableAcceptsRunningExecutable(t *testing.T) {
	// Guards against the replaceability probe reporting a false negative for the executable
	// of the running process, which is locked against writes on Windows but still renamable.
	exe, err := os.Executable()
	require.NoError(t, err)

	assert.NoError(t, checkReplaceable(exe))
}

func TestCheckWritableRemovesStaleUpdateFiles(t *testing.T) {
	dir := t.TempDir()
	exe := writeExe(t, dir)

	stale := []string{
		filepath.Join(dir, ".metaplay.exe.new"),
		filepath.Join(dir, ".metaplay.exe.old"),
	}
	for _, path := range stale {
		require.NoError(t, os.WriteFile(path, []byte("leftover"), 0o644))
	}

	require.NoError(t, CheckWritable(exe))

	for _, path := range stale {
		assert.NoFileExists(t, path, "leftover from an earlier update should be removed")
	}
}

func TestCheckWritableRejectsMissingDirectory(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "does-not-exist", "metaplay.exe")

	assert.Error(t, CheckWritable(exe))
}

func TestRemoveStaleUpdateFilesIgnoresMissingFiles(t *testing.T) {
	assert.NoError(t, removeStaleUpdateFiles(filepath.Join(t.TempDir(), "metaplay.exe")))
}
