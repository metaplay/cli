//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockFile takes an exclusive lock on file without waiting, and reports
// whether it got it. flock, not fcntl: an fcntl lock belongs to the process, so
// two goroutines of one process would both hold it.
func tryLockFile(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

// cannotCreateLockFile reports whether opening the lock file failed because no
// lock file can be made here, rather than by some passing fault.
func cannotCreateLockFile(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, unix.EROFS)
}

// lockUnsupported reports whether the filesystem offers no locks, as NFS
// without a lock daemon or some FUSE filesystems do.
func lockUnsupported(err error) bool {
	return errors.Is(err, unix.ENOLCK) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP)
}
