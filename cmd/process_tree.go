/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os/exec"

	"github.com/metaplay/cli/internal/interrupt"
)

// startCmd starts cmd with startProcessTree and returns a cleanup function that callers should
// defer.
//
// A force-quit ends the CLI at once, without waiting for cmd. So until cleanup is called, a
// force-quit first kills cmd, as WaitDelay would at the end of the grace period, and cmd does not
// keep running after the CLI. On Unix this kills cmd's process only, not the processes it started.
// On Windows the Job Object ends those when the CLI exits.
func startCmd(cmd *exec.Cmd) (func(), error) {
	cleanupTree, err := startProcessTree(cmd)
	if err != nil {
		return nil, err
	}
	removeForceQuitStep := interrupt.OnForceQuit(func() { _ = cmd.Process.Kill() })
	return func() {
		removeForceQuitStep()
		cleanupTree()
	}, nil
}
