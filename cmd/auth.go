/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"github.com/spf13/cobra"
)

// authCmd represents the auth command
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate to Metaplay cloud",
	Long: `Commands related to authenticating with Metaplay cloud.
Supports sign in via a browser for human users and using a secret for machine users.`,
}

func init() {
	rootCmd.AddCommand(authCmd)
}
