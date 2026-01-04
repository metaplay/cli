/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

var databaseCmd = &cobra.Command{
	Use:     "database",
	Aliases: []string{"db"},
	Short:   "Database management commands",
	Long:    "Commands for managing and interacting with game server databases",
	RunE:    requireSubcommand(),
}

func init() {
	rootCmd.AddCommand(databaseCmd)
}
