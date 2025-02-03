/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

// \todo Add a way to update existing secrets (add, update, and remove fields).
// \todo Update SDK documentation to show how to use Kubernetes secrets.

// secrets is a group of commands to manage Kubernetes secrets.
var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "[experimental] Manage Kubernetes secrets of an environment",
}

func init() {
	rootCmd.AddCommand(secretsCmd)
}
