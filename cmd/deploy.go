/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

// deployCmd includes commands for deploying server and bots to the cloud.
var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy server or bots into the cloud",
}

func init() {
	rootCmd.AddCommand(deployCmd)
}
