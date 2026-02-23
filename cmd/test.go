/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

// testCmd is the root for all "test" related subcommands.
var testCmd = &cobra.Command{
	Use:     "test",
	Aliases: []string{"t"},
	Short:   "Developer testing and verification commands",
	Long:    "Commands intended for developer testing, verification, and internal diagnostics.",
}

func init() {
	rootCmd.AddCommand(testCmd)
}
