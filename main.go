/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/metaplay/cli/cmd"
)

func main() {
	// Create a context that cancels on SIGINT (Ctrl+C) or SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// On the first signal: notify the user that graceful shutdown is in
	// progress, then call stop() to unregister signal.Notify so a second
	// Ctrl+C falls through to Go's default handler and terminates the
	// process immediately. NotifyContext doesn't do this by itself — its
	// goroutine consumes the first signal and exits, leaving subsequent
	// signals to queue silently in the channel buffer.
	//
	// cancelMsg unregisters the callback on normal exit so we don't print
	// the message when the command completes successfully.
	cancelMsg := context.AfterFunc(ctx, func() {
		fmt.Fprintln(os.Stderr, "\nInterrupted, cleaning up... (press Ctrl+C again to force quit)")
		stop()
	})
	defer cancelMsg()

	cmd.ExecuteContext(ctx)
}
