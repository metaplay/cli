//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package pathutil

import (
	"os"
	"path/filepath"
)

// GetExecutablePath returns the path of the executable file with all symlinks resolved.
func GetExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}

	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}

	return exe, nil
}
