/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

var migrateR32Cmd = &cobra.Command{
	Use:   "r32",
	Short: "Upgrading SDK to Release 32.0",
	// Long:  trimIndent(``),
}

func init() {
	migrateCmd.AddCommand(migrateR32Cmd)
}
