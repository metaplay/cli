/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/metaplay/cli/cmd"
	"github.com/metaplay/cli/internal/interrupt"
)

func main() {
	// The command's context is cancelled on SIGINT, SIGTERM and a Ctrl+C key press in a TUI. A
	// second Ctrl+C terminates the process at once.
	ctx, stop := interrupt.NotifyContext(context.Background(), func() {
		fmt.Fprintln(os.Stderr, "\nInterrupted, cleaning up... (press Ctrl+C again to force quit)")
	})
	defer stop()

	cmd.ExecuteContext(ctx)
}
