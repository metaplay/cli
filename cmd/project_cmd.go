/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	Use:     "project",
	Aliases: []string{"proj"},
	Short:   "Manage Metaplay projects",
	Long: trimIndent(`
		Commands for managing Metaplay projects.
		These commands help you initialize, configure, and manage your Metaplay projects.
	`),
}

func init() {
	rootCmd.AddCommand(projectCmd)
}
