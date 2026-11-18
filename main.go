/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package main

import (
	"context"
	"fmt"
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
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	// On the first interrupt: notify the user that graceful shutdown is in progress, then call
	// stop() to unregister signal.Notify so a second Ctrl+C falls through to Go's default handler
	// and terminates the process immediately. NotifyContext doesn't do this by itself — its
	// goroutine consumes the first signal and exits, leaving subsequent signals to queue silently
	// in the channel buffer. The message comes before the cancellation, so it precedes anything the
	// command prints as it stops.
	var interruptOnce sync.Once
	interrupt := func() {
		interruptOnce.Do(func() {
			fmt.Fprintln(os.Stderr, "\nInterrupted, cleaning up... (press Ctrl+C again to force quit)")
			stop()
			cancel()
		})
	}
	tui.SetInterruptHandler(interrupt)

	// stopWatchingSignals unregisters the callback on normal exit, so nothing is printed when the
	// command completes successfully.
	stopWatchingSignals := context.AfterFunc(signalCtx, interrupt)
	defer stopWatchingSignals()

	cmd.ExecuteContext(ctx)
}
