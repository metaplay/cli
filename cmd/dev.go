/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Commands for local development",
	RunE:  requireSubcommand(),
}

func init() {
	rootCmd.AddCommand(devCmd)
}
