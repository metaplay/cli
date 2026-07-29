//go:build windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"io/fs"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chocolateyLikeRights is every right except delete (D) and delete-child (DC): files may be
// created in the directory but existing ones may not be removed or renamed. This is the shape
// of C:\ProgramData\chocolatey\lib, which is the install layout this whole check exists for.
const chocolateyLikeRights = `:(OI)(CI)(RD,WD,AD,REA,WEA,X,RA,WA,S)`

// restrictDirectory applies chocolateyLikeRights to dir and registers a cleanup that restores
// full control. Reports false if icacls is unavailable or the filesystem ignores ACLs.
func restrictDirectory(t *testing.T, dir string) bool {
	t.Helper()

	user := os.Getenv("USERNAME")
	if user == "" {
		t.Log("USERNAME is not set, cannot construct an ACL")
		return false
	}

	out, err := exec.Command("icacls", dir, "/inheritance:r", "/grant", user+chocolateyLikeRights).CombinedOutput()
	if err != nil {
		t.Logf("icacls failed: %v: %s", err, out)
		return false
	}

	// Must run before t.TempDir()'s own cleanup — cleanups run in reverse registration order —
	// or the restricted tree cannot be removed and teardown fails with a confusing error.
	t.Cleanup(func() {
		if out, err := exec.Command("icacls", dir, "/grant", user+":(OI)(CI)F", "/t", "/c").CombinedOutput(); err != nil {
			t.Errorf("failed to restore ACLs on %s, temp tree may leak: %v: %s", dir, err, out)
		}
	})
	return true
}

// blockDeletion makes leftover undeletable while keeping exe replaceable, so the leftover path
// can be exercised without tripping the earlier replaceability check.
func blockDeletion(t *testing.T, dir, exe, _ string) (func(), bool) {
	t.Helper()

	if !restrictDirectory(t, dir) {
		return func() {}, false
	}

	// Grant DELETE on the executable only, so checkReplaceable still passes for it.
	user := os.Getenv("USERNAME")
	if out, err := exec.Command("icacls", exe, "/grant", user+":(D,WDAC)").CombinedOutput(); err != nil {
		t.Logf("icacls on the executable failed: %v: %s", err, out)
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
	if !restrictDirectory(t, dir) {
		t.Skip("cannot construct a restrictive ACL in this environment")
	}

	// Created after the ACL change so it inherits the restricted rights.
	exe := writeExe(t, dir)

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
