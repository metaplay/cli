/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/metaplay/cli/cmd"
	"github.com/metaplay/cli/internal/tui"
)

func main() {
	// Cancel the command's context on SIGINT (Ctrl+C) or SIGTERM, and on Ctrl+C pressed in a TUI,
	// which receives it as a key press, not as SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// On the first interrupt, tell the user that cleanup is in progress, then call stop(). It
	// cancels ctx and unregisters the signal handler, so a second Ctrl+C falls through to Go's
	// default handler and terminates the process immediately. NotifyContext does not do this by
	// itself: it consumes the first signal and leaves later ones queued in its channel.
	var interruptOnce sync.Once
	interrupt := func() {
		interruptOnce.Do(func() {
			tui.PrintNotice("\nInterrupted, cleaning up... (press Ctrl+C again to force quit)")
			stop()
		})
	}
	tui.SetInterruptHandler(interrupt)

	// stopWatchingSignals unregisters the callback on normal exit, so nothing is printed when the
	// command completes successfully.
	stopWatchingSignals := context.AfterFunc(ctx, interrupt)
	defer stopWatchingSignals()

	cmd.ExecuteContext(ctx)
}
