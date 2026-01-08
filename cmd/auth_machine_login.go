/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type authMachineLoginOpts struct {
	UsePositionalArgs

	argAuthProvider string
	flagCredentials string
}

func init() {
	o := authMachineLoginOpts{}

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

func (o *authMachineLoginOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *authMachineLoginOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve auth provider.
	authProvider, err := getAuthProvider(project, o.argAuthProvider)
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
			return clierrors.NewUsageError("Missing credentials for machine login").
				WithSuggestion("Set the METAPLAY_CREDENTIALS environment variable to the credentials value from the developer portal")
		} else {
			credentials = envCredentials
		}
	}

	clientID, clientSecret, ok := strings.Cut(credentials, "+")
	if !ok {
		return clierrors.NewUsageError("Invalid credentials format").
			WithSuggestion("Copy-paste the credentials value from the developer portal verbatim")
	}

	err = auth.MachineLogin(authProvider, clientID, clientSecret)
	if err != nil {
		return clierrors.Wrap(err, "Machine login failed").
			WithSuggestion("Check your credentials and try again")
	}

	return nil
}
