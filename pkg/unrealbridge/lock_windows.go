//go:build windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package unrealbridge

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// lockFileExclusive acquires an exclusive lock on the file at path, retrying until the
// timeout elapses (Windows implementation: LockFileEx over the whole file).
func lockFileExclusive(path string, timeout time.Duration) (func(), error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("failed to encode lock file path %s: %w", path, err)
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.CREATE_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", path, err)
	}

	// LockFileEx with LOCKFILE_EXCLUSIVE_LOCK blocks; emulate a timeout with
	// LOCKFILE_FAIL_IMMEDIATELY and a retry loop (same behavior as the flock path).
	detect := &windows.Overlapped{}
	const lockAllBytes = 0xFFFFFFFF
	deadline := time.Now().Add(timeout)
	for {
		err := lockFileEx(
			syscall.Handle(handle),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0,
			lockAllBytes, 0,
			detect)
		if err == nil {
			return func() {
				_ = unlockFileEx(syscall.Handle(handle), 0, lockAllBytes, 0, &windows.Overlapped{})
				_ = windows.CloseHandle(handle)
			}, nil
		}
		if err != windows.ERROR_LOCK_VIOLATION {
			windows.CloseHandle(handle)
			return nil, fmt.Errorf("failed to lock %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			windows.CloseHandle(handle)
			return nil, fmt.Errorf("another metaplay-bridge run holds %s for over %s", path, timeout)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func lockFileEx(handle syscall.Handle, flags, reserved, lockLow, lockHigh uint32, overlapped *windows.Overlapped) error {
	r1, _, err := syscall.SyscallN(
		procLockFileEx.Addr(),
		uintptr(handle),
		uintptr(flags),
		uintptr(reserved),
		uintptr(lockLow),
		uintptr(lockHigh),
		uintptr(unsafe.Pointer(overlapped)))
	if r1 == 0 {
		return err
	}
	return nil
}

func unlockFileEx(handle syscall.Handle, reserved, unlockLow, unlockHigh uint32, overlapped *windows.Overlapped) error {
	r1, _, err := syscall.SyscallN(
		procUnlockFileEx.Addr(),
		uintptr(handle),
		uintptr(reserved),
		uintptr(unlockLow),
		uintptr(unlockHigh),
		uintptr(unsafe.Pointer(overlapped)))
	if r1 == 0 {
		return err
	}
	return nil
}

var (
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)
