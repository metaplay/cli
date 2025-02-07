/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type DeleteSecretOpts struct {
	argEnvironment string
	argSecretName  string
}

func init() {
	o := DeleteSecretOpts{}

	cmd := &cobra.Command{
		Use:   "delete ENVIRONMENT NAME [flags]",
		Short: "[preview] Delete a user secret in the target environment",
		Run:   runCommand(&o),
		Long: trimIndent(`
			PREVIEW: This command is in preview and subject to change!

			Delete a user-created secret with the given name from the target environment.

			Related commands:
			- 'metaplay secrets create ENVIRONMENT NAME ...' to create a new user secret.
			- 'metaplay secrets list ENVIRONMENT ...' to list all user secrets.
			- 'metaplay secrets show ENVIRONMENT NAME ...' to show the contents of a user secret.
		`),
		Example: trimIndent(`
			# Delete the secret 'user-mysecret' from the environment 'my-environment'.
			metaplay secrets delete my-environment user-mysecret
		`),
	}

	secretsCmd.AddCommand(cmd)
}

func (o *DeleteSecretOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("exactly two arguments must be provided, got %d", len(args))
	}

	// Store arguments.
	o.argEnvironment = args[0]
	o.argSecretName = args[1]

	return nil
}

func (o *DeleteSecretOpts) Run(cmd *cobra.Command) error {
	// Ensure the user is logged in
	tokenSet, err := tui.RequireLoggedIn(cmd.Context())
	if err != nil {
		return err
	}

	// Resolve environment.
	envConfig, err := resolveEnvironment(tokenSet, o.argEnvironment)
	if err != nil {
		return err
	}

	// Create TargetEnvironment.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Delete the secret.
	err = targetEnv.DeleteSecret(cmd.Context(), o.argSecretName)
	if err != nil {
		return err
	}

	log.Info().Msgf("Secret %s deleted", o.argSecretName)
	return nil
}
