/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

var imageCmd = &cobra.Command{
	Use:   "image",
	Short: "Commands for managing server Docker images",
}

func init() {
	rootCmd.AddCommand(imageCmd)
}
