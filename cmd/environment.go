/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

// environmentCmd represents the environment command group.
var environmentCmd = &cobra.Command{
	Use:     "environment",
	Aliases: []string{"env"},
	Short:   "Commands for managing Metaplay cloud environments",
}

func init() {
	rootCmd.AddCommand(environmentCmd)
}
