//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"os"
	"syscall"
)

func isSocketPermissionError(err error) bool {
	return errors.Is(err, os.ErrPermission)
}

func isSocketNetworkUnreachableError(err error) bool {
	return errors.Is(err, syscall.ENETUNREACH)
}
