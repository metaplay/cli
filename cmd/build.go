/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

// buildCmd is a group of commands to build backend components locally
var buildCmd = &cobra.Command{
	Use:     "build",
	Aliases: []string{"b"},
	Short:   "Build game server components locally",
}

func init() {
	rootCmd.AddCommand(buildCmd)
}
