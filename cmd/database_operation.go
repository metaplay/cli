/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

// databaseOperationCmd is the parent command for inspecting async database
// operations (snapshot create, snapshot delete, rollback).
var databaseOperationCmd = &cobra.Command{
	Use:   "operation",
	Short: "Inspect async database operations",
	Long: `Inspect async database operations such as in-progress snapshot
creations, snapshot deletions, and point-in-time rollbacks.`,
}

func init() {
	databaseCmd.AddCommand(databaseOperationCmd)
}
