/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Commands for local development",
}

func init() {
	rootCmd.AddCommand(devCmd)
}
