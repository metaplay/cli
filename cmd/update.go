/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update components or tools in the project",
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
