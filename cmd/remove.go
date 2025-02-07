/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

// removeCmd includes commands for removing components from the cloud.
var removeCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove deployed components from the cloud",
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
