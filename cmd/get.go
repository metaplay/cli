/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

var getCmd = &cobra.Command{
	Use:   "get",
	Short: "Get information about cloud resources",
	RunE:  requireSubcommand(),
}

func init() {
	rootCmd.AddCommand(getCmd)
}
