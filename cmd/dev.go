/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

// devCmd is used for internal development-only commands.
var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Development-only commands",
}

func init() {
	devCmd.Hidden = true
	rootCmd.AddCommand(devCmd)
}
