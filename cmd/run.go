/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

// runCmd is a group of commands to run backend components locally
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run components of the backend locally",
}

func init() {
	rootCmd.AddCommand(runCmd)
}
