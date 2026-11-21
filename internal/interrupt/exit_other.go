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

// ignoredAtStart holds the signals the process started with ignored, such as SIGINT for a
// background job of a script. signal.Reset restores the ignore, so re-raising one does nothing.
// signal.Ignored sees the ignore only before signal.Notify, hence a package variable.
var ignoredAtStart = map[syscall.Signal]bool{
	syscall.SIGINT:  signal.Ignored(syscall.SIGINT),
	syscall.SIGTERM: signal.Ignored(syscall.SIGTERM),
}

// exitOnSignal ends the process with sig, as Go's default handler would. A shell sees a process
// that a signal ended, so a script or a loop that runs the CLI stops too, which an exit with
// ExitCode would not do.
func exitOnSignal(sig os.Signal) {
	if s, ok := sig.(syscall.Signal); ok && !ignoredAtStart[s] {
		signal.Reset(s)
		if syscall.Kill(syscall.Getpid(), s) == nil {
			// The signal is delivered asynchronously, within milliseconds, and ends the process
			// during this wait
			time.Sleep(time.Second)
		}
	}
	os.Exit(ExitCode)
}
