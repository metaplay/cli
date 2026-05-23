/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

//go:build !windows

package cmd

import "os/exec"

// killOnExit is a no-op on Unix: process groups + SIGTERM/SIGKILL already
// handle descendant cleanup naturally. The Windows variant attaches the
// child to a Job Object so killing the parent atomically tears down the
// whole subprocess tree.
func killOnExit(_ *exec.Cmd) func() {
	return func() {}
}
