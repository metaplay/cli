//go:build windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"fmt"

	"github.com/metaplay/cli/internal/pathutil"
	"golang.org/x/sys/windows"
)

// checkReplaceable reports whether the current process holds the DELETE right on path, which
// is what Windows requires to rename the file out of the way during the binary swap. The
// install directory being writable is not enough: files there commonly inherit a read-only
// ACE for BUILTIN\Users (eg, C:\ProgramData\chocolatey\lib), which permits creating new files
// but not replacing existing ones.
//
// Opening a handle with DELETE access does not delete anything — that would require
// FILE_FLAG_DELETE_ON_CLOSE — so this is a safe, non-destructive probe.
func checkReplaceable(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	// Share everything so the probe cannot fail over a share-mode conflict with another handle.
	handle, err := windows.CreateFile(
		p,
		windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		// The errno is wrapped as-is: syscall.Errno already satisfies errors.Is for
		// fs.ErrPermission (ERROR_ACCESS_DENIED) and fs.ErrNotExist, and keeping it preserves
		// which failure it actually was.
		return fmt.Errorf("cannot replace %s: %w", pathutil.ForDisplay(path), err)
	}
	_ = windows.CloseHandle(handle)

	return nil
}
