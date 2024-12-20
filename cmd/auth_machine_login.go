package cmd

import (
	"os"
	"strings"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var flagCredentials string

var machineLoginCmd = &cobra.Command{
	Use:   "machine-login",
	Short: "Sign in to Metaplay cloud using a machine account",
	Run:   runMachineLoginCmd,
}

func init() {
	authCmd.AddCommand(machineLoginCmd)
	machineLoginCmd.Flags().StringVar(&flagCredentials, "dev-credentials", "", "Machine login credentials (prefer passing credentials via the environment variable METAPLAY_CREDENTIALS for better security)")
}

func runMachineLoginCmd(cmd *cobra.Command, args []string) {
	var credentials string

	if flagCredentials != "" {
		log.Debug().Msg("Using command line credentials for machine login")
		credentials = flagCredentials
	} else {
		log.Debug().Msg("Using environment variable METAPLAY_CREDENTIALS for machine login")
		if envCredentials, ok := os.LookupEnv("METAPLAY_CREDENTIALS"); !ok {
			log.Error().Msg("Unable to find the credentials, the environment variable METAPLAY_CREDENTIALS is not defined!")
			os.Exit(2)
		} else {
			credentials = envCredentials
		}
	}

	if clientId, clientSecret, ok := strings.Cut(credentials, "+"); !ok {
		log.Error().Msg("Invalid format for credentials, you should copy-paste the value from the developer portal verbatim")
		os.Exit(2)
	} else {
		err := auth.MachineLogin(clientId, clientSecret)
		if err != nil {
			log.Error().Msgf("Machine login failed: %s", err)
			os.Exit(1)
		}
	}
}