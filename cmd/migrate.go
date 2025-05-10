/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

// migrateCmd represents the migrate command
var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "SDK version upgrade related migrations commands",
	Long: trimIndent(`
		Helper commands for performing various migrations related to Metaplay SDK version upgrades.
	`),
	Example: trimIndent(`
		# Show migration commands when upgrading to Metaplay SDK 32.0.
		metaplay migrate r32
	`),
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
