//go:build windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isSocketPermissionError(err error) bool {
	return errors.Is(err, windows.WSAEACCES)
}

func isSocketNetworkUnreachableError(err error) bool {
	return errors.Is(err, windows.WSAENETUNREACH)
}
