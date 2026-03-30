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
	// After the first signal, Go restores default signal handling,
	// so a second Ctrl+C will terminate the process immediately.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Notify the user that graceful shutdown is in progress after Ctrl+C.
	// The returned function unregisters the callback; we defer it so it runs
	// before stop() (LIFO), preventing the message on normal exit.
	cancelMsg := context.AfterFunc(ctx, func() {
		fmt.Fprintln(os.Stderr, "\nInterrupted, cleaning up... (press Ctrl+C again to force quit)")
	})
	defer cancelMsg()

	cmd.ExecuteContext(ctx)
}
