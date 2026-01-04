/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

var imageCmd = &cobra.Command{
	Use:   "image",
	Short: "Commands for managing server Docker images",
	RunE:  requireSubcommand(),
}

func init() {
	rootCmd.AddCommand(imageCmd)
}
