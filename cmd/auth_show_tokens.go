/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"encoding/json"
	"os"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type ShowTokensOpts struct {
}

func init() {
	o := ShowTokensOpts{}

	cmd := &cobra.Command{
		Use:   "show-tokens",
		Short: "Print the active tokens as JSON to stdout",
		Long:  `Print the currently active authentication tokens to stdout.`,
		Run:   runCommand(&o),
	}

	cmd.Hidden = true
	authCmd.AddCommand(cmd)
}

func (o *ShowTokensOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *ShowTokensOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}
	authProvider := getAuthProvider(project)

	// Load tokenSet from keyring & refresh if needed.
	tokenSet, err := auth.LoadAndRefreshTokenSet(authProvider)
	if err != nil {
		return err
	}

	// Handle missing tokens (not logged in).
	if tokenSet == nil {
		log.Warn().Msg("Not logged in! Sign in first with 'metaplay auth login' or 'metaplay auth machine-login'")
		os.Exit(1)
	}

	// Marshal tokenSet to JSON.
	bytes, err := json.MarshalIndent(tokenSet, "", "  ")
	if err != nil {
		log.Panic().Msgf("failed to serialize tokens into JSON: %v", err)
	}

	// Print as string.
	log.Info().Msg(string(bytes))
	return nil
}
