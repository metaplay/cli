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

// sigintIgnoredAtStart is true when the process started with SIGINT ignored, as a background job
// of a script does. signal.Reset restores that ignore, so re-raising SIGINT then does nothing. Go
// keeps an inherited ignore only for SIGHUP and SIGINT, so SIGTERM needs no such check.
// signal.Ignored sees the ignore only before signal.Notify, hence a package variable.
var sigintIgnoredAtStart = signal.Ignored(syscall.SIGINT)

// exitOnSignal ends the process with sig, as Go's default handler would. A shell sees a process
// that a signal ended, so a script or a loop that runs the CLI stops too, which an exit with
// ExitCode would not do.
func exitOnSignal(sig os.Signal) {
	if s, ok := sig.(syscall.Signal); ok && (s != syscall.SIGINT || !sigintIgnoredAtStart) {
		signal.Reset(s)
		if syscall.Kill(syscall.Getpid(), s) == nil {
			// The signal is delivered asynchronously, within milliseconds, and ends the process
			// during this wait
			time.Sleep(time.Second)
		}
	}
	os.Exit(ExitCode)
}
