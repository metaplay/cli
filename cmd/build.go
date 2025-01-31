/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

// buildCmd is a group of commands to build backend components locally
var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build components of the backend locally",
}

func init() {
	rootCmd.AddCommand(buildCmd)
}
