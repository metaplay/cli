//go:build windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile takes an exclusive lock on the first byte of file without
// waiting, and reports whether it got it.
func tryLockFile(file *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

// cannotCreateLockFile reports whether opening the lock file failed because no
// lock file can be made here, rather than by some passing fault.
func cannotCreateLockFile(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, windows.ERROR_WRITE_PROTECT)
}

// lockUnsupported reports whether the filesystem offers no locks.
func lockUnsupported(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION)
}
