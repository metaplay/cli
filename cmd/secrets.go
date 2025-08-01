/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
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
	Short: "[preview] Manage Kubernetes secrets of an environment",
}

func init() {
	rootCmd.AddCommand(secretsCmd)
}
