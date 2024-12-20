package cmd

import (
	"os"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// logoutCmd represents the logout command
var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Sign out from Metaplay cloud",
	Long:  `Delete the locally persisted credentials to sign out from Metaplay cloud.`,
	Run:   runLogoutCmd,
}

func init() {
	authCmd.AddCommand(logoutCmd)
}

func runLogoutCmd(cmd *cobra.Command, args []string) {
	// Delete the token set.
	err := auth.DeleteTokenSet()
	if err != nil {
		log.Error().Msgf("Failed to delete tokens: %v", err)
		os.Exit(1)
	}

	log.Info().Msg("Successfully logged out!")
}
