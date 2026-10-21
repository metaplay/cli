//go:build windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests reproduce the ACL shape of C:\ProgramData\chocolatey\lib, where files may be
// created but existing ones may not be replaced.
//
// Deny ACEs are used rather than a restricted grant: CI runs as an elevated administrator, and
// a grant-only restriction leaves the Administrators ACE in place so the restriction does not
// bite. Deny ACEs are evaluated ahead of allows for every SID in the token. Even so, each setup
// verifies the denial actually took effect and skips if it did not — a test of a permission
// failure that silently starts passing is worse than one that is absent.

// icaclsUser returns the account name to write ACEs for.
func icaclsUser(t *testing.T) (string, bool) {
	t.Helper()
	user := os.Getenv("USERNAME")
	if user == "" {
		t.Log("USERNAME is not set, cannot construct an ACL")
		return "", false
	}
	return user, true
}

// denyRights adds a deny ACE for the current user on path and registers its removal, which must
// happen before t.TempDir()'s cleanup or the tree cannot be deleted. Cleanups run in reverse
// registration order, so registering here is correct.
func denyRights(t *testing.T, path, rights string) bool {
	t.Helper()

	user, ok := icaclsUser(t)
	if !ok {
		return false
	}

	if out, err := exec.Command("icacls", path, "/deny", user+":("+rights+")").CombinedOutput(); err != nil {
		t.Logf("icacls /deny %s on %s failed: %v: %s", rights, path, err, out)
		return false
	}

	t.Cleanup(func() {
		// The probe below deliberately tries to delete files, so the target may be gone.
		if _, statErr := os.Lstat(path); errors.Is(statErr, fs.ErrNotExist) {
			return
		}
		if out, err := exec.Command("icacls", path, "/remove:d", user).CombinedOutput(); err != nil {
			t.Errorf("failed to drop the deny ACE on %s, temp tree may leak: %v: %s", path, err, out)
		}
	})
	return true
}

// deletionIsBlocked reports whether the deny ACEs actually prevent deletion here, by trying it
// on a throwaway file rather than on the file the test cares about. The caller must already have
// denied delete-child on dir: without that, the parent's rights permit deleting any file in it
// regardless of the file's own DACL, and a per-file deny proves nothing.
func deletionIsBlocked(t *testing.T, dir string) bool {
	t.Helper()

	canary := filepath.Join(dir, "canary")
	require.NoError(t, os.WriteFile(canary, []byte("canary"), 0o644))

	if !denyRights(t, canary, "D") {
		return false
	}
	if err := os.Remove(canary); err == nil {
		t.Log("deny ACEs do not prevent deletion in this environment")
		return false
	}
	return true
}

// makeUnreplaceable denies deletion of exe and delete-child on dir, so the executable cannot be
// renamed out of the way while the directory still accepts new files.
func makeUnreplaceable(t *testing.T, dir, exe string) bool {
	t.Helper()

	// Applied to the directory itself, not inherited, so files created in it stay deletable.
	if !denyRights(t, dir, "DC") || !denyRights(t, exe, "D") {
		return false
	}
	if err := checkReplaceable(exe); err == nil {
		t.Log("the executable is still replaceable despite the deny ACEs")
		return false
	}
	return true
}

// blockDeletion makes leftover undeletable while keeping exe replaceable, so the leftover path
// can be exercised without tripping the earlier replaceability check.
func blockDeletion(t *testing.T, dir, exe, leftover string) (func(), bool) {
	t.Helper()

	// Deny delete-child on the directory first, or the parent's rights alone would allow removing
	// the leftover. The executable keeps its inherited DELETE, so it stays replaceable.
	if !denyRights(t, dir, "DC") {
		return func() {}, false
	}
	if !deletionIsBlocked(t, dir) {
		return func() {}, false
	}
	if !denyRights(t, leftover, "D") {
		return func() {}, false
	}
	if err := checkReplaceable(exe); err != nil {
		t.Logf("the executable became unreplaceable, which is not the case under test: %v", err)
		return func() {}, false
	}
	return func() {}, true
}

func TestCheckReplaceableAcceptsRunningExecutable(t *testing.T) {
	// The probe must not report a false negative for the executable of the running process,
	// which Windows locks against writes while still allowing a rename. This is why the probe
	// asks for DELETE rather than write access.
	exe, err := os.Executable()
	require.NoError(t, err)

	assert.NoError(t, checkReplaceable(exe))
}

func TestCheckReplaceableRejectsUnreplaceableBinary(t *testing.T) {
	dir := t.TempDir()
	exe := writeExe(t, dir)

	if !makeUnreplaceable(t, dir, exe) {
		t.Skip("cannot make a binary unreplaceable in this environment")
	}

	err := checkReplaceable(exe)
	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrPermission)

	// EnsureReplaceable must surface this as the permission problem it is, and must not blame a
	// leftover file — the directory probe alone would pass here, which is the whole point.
	err = EnsureReplaceable(exe)
	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrPermission)
	assert.NotErrorIs(t, err, ErrLeftoverInUse)
}

func TestCheckReplaceableReportsMissingFileAsNotExist(t *testing.T) {
	// A vanished executable must not be reported as a permission problem.
	err := checkReplaceable(writeExe(t, t.TempDir()) + ".missing")

	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrNotExist)
	assert.NotErrorIs(t, err, fs.ErrPermission)
}

func TestEnsureReplaceableReportsBlockedLeftoverAsInUse(t *testing.T) {
	dir := t.TempDir()
	exe := writeExe(t, dir)
	leftover := updateTempPaths(exe)[1]
	require.NoError(t, os.WriteFile(leftover, []byte("leftover"), 0o644))

	restore, blocked := blockDeletion(t, dir, exe, leftover)
	if !blocked {
		t.Skip("cannot make a file undeletable in this environment")
	}
	defer restore()

	// Removing the leftover fails with ERROR_ACCESS_DENIED here, the same errno a mapped
	// running image produces. It must still be classified as in-use, not as an unwritable
	// install, or the hint tells the user to elevate for something elevation cannot fix.
	assertBlockedLeftover(t, EnsureReplaceable(exe))
}
