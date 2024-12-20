package cmd

import (
	"os"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// loginCmd logs a natural user to Metaplay Auth using the browser.
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to your Metaplay account using the browser",
	Run:   runLoginCmd,
}

func init() {
	authCmd.AddCommand(loginCmd)
}

func runLoginCmd(cmd *cobra.Command, args []string) {
	err := auth.LoginWithBrowser()
	if err != nil {
		log.Error().Msgf("Failed to login to Metaplay Auth: %v", err)
		os.Exit(1)
	}
}
