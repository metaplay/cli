//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"os/exec"
	"syscall"
)

// startCmd starts the command and returns a cleanup function that callers
// should defer. On Unix it's a thin wrapper over cmd.Start — process group
// + SIGTERM/SIGKILL naturally tear down descendants. The Windows variant
// goes through a job-object dance to atomically kill the whole subprocess
// tree on cancellation.
func startCmd(cmd *exec.Cmd) (func(), error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() {}, nil
}

// wasKilledBySignal reports whether the process was terminated by a signal
// rather than exiting normally. Relies on syscall.WaitStatus.Signaled() —
// more reliable than checking ExitCode == -1, which also fires for processes
// that haven't yet exited.
func wasKilledBySignal(ps *os.ProcessState) bool {
	if ps == nil {
		return false
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if !ok {
		return false
	}
	return ws.Signaled()
}
