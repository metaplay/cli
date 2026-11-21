//go:build !windows

/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package interrupt

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// exitOnSignal ends the process with sig, as Go's default handler would. A shell sees a process
// that a signal ended, so a script or a loop that runs the CLI stops too, which an exit with
// ExitCode would not do.
func exitOnSignal(sig os.Signal) {
	if s, ok := sig.(syscall.Signal); ok {
		signal.Reset(s)
		if syscall.Kill(syscall.Getpid(), s) == nil {
			// The signal is delivered asynchronously and ends the process during this wait
			time.Sleep(5 * time.Second)
		}
	}
	os.Exit(ExitCode)
}
