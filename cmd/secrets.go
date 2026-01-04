/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

// secrets is a group of commands to manage Kubernetes secrets.
var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage Kubernetes secrets of an environment",
	RunE:  requireSubcommand(),
}

func init() {
	rootCmd.AddCommand(secretsCmd)
}
