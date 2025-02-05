/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

var debugCmd = &cobra.Command{
	Use:     "debug",
	Aliases: []string{"d"},
	Short:   "Debug and diagnostic commands",
	Long:    "Commands for debugging and diagnostics of running game servers",
}

func init() {
	rootCmd.AddCommand(debugCmd)
}
