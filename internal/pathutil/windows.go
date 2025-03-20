//go:build windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package pathutil

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"syscall"
)

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
	defer file.Close()

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
