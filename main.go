/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/metaplay/cli/cmd"
)

func main() {
	// Create a context that cancels on SIGINT (Ctrl+C) or SIGTERM
	// This ensures graceful cleanup when the user interrupts the CLI
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd.ExecuteContext(ctx)
}
