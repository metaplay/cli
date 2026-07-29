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
	"golang.org/x/sys/windows"
)

// lockFile opens path sharing reads only, which blocks removal on Windows the same way a
// mapped executable image does.
func lockFile(t *testing.T, path string) (func(), bool) {
	t.Helper()

	p, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)

	handle, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	require.NoError(t, err)

	return func() { _ = windows.CloseHandle(handle) }, true
}

func TestCheckReplaceableReportsPermissionDenied(t *testing.T) {
	user := os.Getenv("USERNAME")
	if user == "" {
		t.Skip("USERNAME is not set")
	}

	// ACL the directory the way C:\ProgramData\chocolatey\lib is: every right except delete (D)
	// and delete-child (DC), so files may be created but existing ones may not be replaced.
	dir := t.TempDir()
	out, err := exec.Command("icacls", dir, "/inheritance:r",
		"/grant", user+":(OI)(CI)(RD,WD,AD,REA,WEA,X,RA,WA,S)").CombinedOutput()
	if err != nil {
		t.Skipf("icacls failed: %v: %s", err, out)
	}

	// Restore full control before t.TempDir()'s cleanup runs — cleanups run in reverse order of
	// registration — or the tree cannot be removed and the test fails during teardown.
	t.Cleanup(func() {
		_ = exec.Command("icacls", dir, "/grant", user+":(OI)(CI)F", "/t", "/c").Run()
	})

	// Created after the ACL change so it inherits the restricted rights.
	exe := writeExe(t, dir)

	// The pre-flight must classify this as a permission problem: the whole hint selection in
	// 'metaplay update cli' keys off fs.ErrPermission.
	err = checkReplaceable(exe)
	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrPermission)

	assert.ErrorIs(t, EnsureReplaceable(exe), fs.ErrPermission)
}
