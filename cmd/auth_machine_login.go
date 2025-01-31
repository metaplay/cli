/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type MachineLoginOpts struct {
	flagCredentials string
}

func init() {
	o := MachineLoginOpts{}

	cmd := &cobra.Command{
		Use:   "machine-login [flags]",
		Short: "Sign in to Metaplay cloud using a machine account",
		Run:   runCommand(&o),
	}
	authCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagCredentials, "dev-credentials", "", "Machine login credentials (prefer passing credentials via the environment variable METAPLAY_CREDENTIALS for better security)")
}

func (o *MachineLoginOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("expecting no arguments, got %d", len(args))
	}

	return nil
}

func (o *MachineLoginOpts) Run(cmd *cobra.Command) error {
	// Resolve credentials to use.
	var credentials string
	if o.flagCredentials != "" {
		log.Debug().Msg("Using command line credentials for machine login")
		credentials = o.flagCredentials
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

	return nil
}
