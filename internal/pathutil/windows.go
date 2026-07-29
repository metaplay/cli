//go:build windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package pathutil

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// ForDisplay strips the \\?\ extended-length prefix that GetExecutablePath returns, so paths
// read naturally in user-facing messages. Only use it for display: the prefix is what allows
// file operations on paths longer than MAX_PATH.
//
// Only the two forms that round-trip to something a user can act on are stripped — a drive
// path and a UNC path. Other device forms such as \\?\Volume{GUID}\ and \\?\GLOBALROOT\ are
// left intact, since stripping those yields a string that looks like a relative path and is
// not usable anywhere.
func ForDisplay(path string) string {
	rest, ok := strings.CutPrefix(path, `\\?\`)
	if !ok {
		return path
	}

	// \\?\UNC\server\share -> \\server\share. The prefix casing is not guaranteed.
	if len(rest) >= 4 && strings.EqualFold(rest[:4], `UNC\`) {
		return `\\` + rest[4:]
	}

	// \\?\C:\... -> C:\...
	if len(rest) >= 3 && isDriveLetter(rest[0]) && rest[1] == ':' && rest[2] == '\\' {
		return rest
	}

	return path
}

func isDriveLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// GetExecutablePath returns the path of the executable file with all symlinks resolved.
func GetExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}

	file, err := os.Open(exe)
	if err != nil {
		return "", fmt.Errorf("failed to open the executable file: %v", err)
	}
	defer func() { _ = file.Close() }()

	// Get the Windows handle
	handle := windows.Handle(file.Fd())

	// Probe call to determine the needed buffer size
	bufSize, err := windows.GetFinalPathNameByHandle(handle, nil, 0, 0)
	if err != nil {
		return "", err
	}

	// Buffer to store the final path
	buf := make([]uint16, bufSize)
	n, err := windows.GetFinalPathNameByHandle(handle, &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return "", fmt.Errorf("failed to get the final path name by handle: %v", err)
	}

	// Convert the buffer to a string
	finalPath := syscall.UTF16ToString(buf[:n])

	return finalPath, nil
}
