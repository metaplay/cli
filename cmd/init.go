/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize project features",
	RunE:  requireSubcommand(),
}

func init() {
	rootCmd.AddCommand(initCmd)
}
