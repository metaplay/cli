//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os/exec"
	"time"
)

// startCmd configures cmd.Cancel/WaitDelay for graceful shutdown, starts
// the command, and returns a cleanup function that callers should defer.
// On Unix it's a thin wrapper over cmd.Start — process group +
// SIGTERM/SIGKILL naturally tear down descendants. The Windows variant
// goes through a job-object dance to atomically kill the whole subprocess
// tree on cancellation.
//
// We replace exec.CommandContext's default Cancel (Process.Kill, which is
// SIGKILL on Unix / TerminateProcess on Windows) with a no-op so the
// OS-delivered Ctrl+C — already broadcast to every process in the
// foreground process group — can drive a graceful shutdown. WaitDelay
// caps the grace window with a force-kill if the child wedges.
func startCmd(cmd *exec.Cmd) (func(), error) {
	cmd.Cancel = func() error { return nil }
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() {}, nil
}
