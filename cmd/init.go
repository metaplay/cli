/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize project features",
}

func init() {
	rootCmd.AddCommand(initCmd)
}
