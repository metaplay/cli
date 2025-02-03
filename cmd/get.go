/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

var getCmd = &cobra.Command{
	Use:   "get",
	Short: "Get information about cloud resources",
}

func init() {
	rootCmd.AddCommand(getCmd)
}
