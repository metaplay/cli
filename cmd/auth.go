package cmd

import (
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate to Metaplay cloud",
	Long: `Commands related to authenticating with Metaplay cloud.
Supports sign in via a browser for human users and using a secret for machine users.`,
}

func init() {
	rootCmd.AddCommand(authCmd)
}
