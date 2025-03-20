/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"os"
	"strings"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type MachineLoginOpts struct {
	UsePositionalArgs

	argAuthProvider string
	flagCredentials string
}

func init() {
	o := MachineLoginOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argAuthProvider, "AUTH_PROVIDER", "Name of the auth provider to use. Defaults to 'metaplay'.")

	cmd := &cobra.Command{
		Use:   "machine-login [AUTH_PROVIDER] [flags]",
		Short: "Sign in to the target authentication provider using a machine account",
		Long: renderLong(&o, `
			Sign in to the target authentication provider using a machine account.

			The default auth provider is 'metaplay'. If you have multiple auth providers configured in your
			'metaplay-project.yaml', you can specify the name of the provider you want to use with the
			argument AUTH_PROVIDER.

			{Arguments}
		`),
		Run: runCommand(&o),
	}
	authCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagCredentials, "dev-credentials", "", "Machine login credentials (prefer passing credentials via the environment variable METAPLAY_CREDENTIALS for better security)")
}

func (o *MachineLoginOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *MachineLoginOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve auth provider.
	authProviderName := coalesceString(o.argAuthProvider, "metaplay")
	authProvider, err := getAuthProvider(project, authProviderName)
	if err != nil {
		return err
	}

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
		err := auth.MachineLogin(authProvider, clientId, clientSecret)
		if err != nil {
			log.Error().Msgf("Machine login failed: %s", err)
			os.Exit(1)
		}
	}

	return nil
}
