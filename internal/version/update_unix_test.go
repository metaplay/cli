//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// blockDeletion drops write permission on the containing directory, which is what prevents
// unlinking a file on Unix. Reports false when that does not actually block removal — running
// as root, or a filesystem that ignores mode bits.
func blockDeletion(t *testing.T, dir, _, _ string) (func(), bool) {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Log("running as root, directory permissions do not block unlink")
		return func() {}, false
	}

	info, err := os.Stat(dir)
	require.NoError(t, err)
	restore := func() { _ = os.Chmod(dir, info.Mode().Perm()) }

	// A canary proves the restriction actually bites before the test relies on it.
	canary := filepath.Join(dir, "canary")
	require.NoError(t, os.WriteFile(canary, []byte("canary"), 0o644))
	require.NoError(t, os.Chmod(dir, 0o500))

	if err := os.Remove(canary); err == nil {
		t.Log("removal is not blocked by directory permissions here")
		restore()
		return func() {}, false
	}

	// t.TempDir() cleanup needs the mode back; cleanups run in reverse registration order, and
	// this one is registered after TempDir's, so it runs first.
	t.Cleanup(restore)
	return restore, true
}

func TestEnsureReplaceableReportsBlockedLeftoverAsInUse(t *testing.T) {
	// On Unix a directory that blocks unlink also blocks create, so EnsureReplaceable fails at
	// the directory probe. The classification contract is therefore asserted directly against
	// removeStaleUpdateFiles; see the Windows test for the end-to-end path.
	dir := t.TempDir()
	exe := writeExe(t, dir)
	leftover := updateTempPaths(exe)[1]
	require.NoError(t, os.WriteFile(leftover, []byte("leftover"), 0o644))

	restore, blocked := blockDeletion(t, dir, exe, leftover)
	if !blocked {
		t.Skip("cannot make a file undeletable in this environment")
	}
	defer restore()

	assertBlockedLeftover(t, removeStaleUpdateFiles(exe))
}
