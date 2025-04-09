/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type DeleteSecretOpts struct {
	UsePositionalArgs

	argEnvironment string
	argSecretName  string
}

func init() {
	o := DeleteSecretOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgument(&o.argSecretName, "NAME", "Name of the secret, e.g., 'user-some-secret'.")

	cmd := &cobra.Command{
		Use:   "delete ENVIRONMENT NAME [flags]",
		Short: "[preview] Delete a user secret in the target environment",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change!

			Delete a user-created secret with the given name from the target environment.

			{Arguments}

			Related commands:
			- 'metaplay secrets create ENVIRONMENT NAME ...' to create a new user secret.
			- 'metaplay secrets list ENVIRONMENT ...' to list all user secrets.
			- 'metaplay secrets show ENVIRONMENT NAME ...' to show the contents of a user secret.
		`),
		Example: renderExample(`
			# Delete the secret 'user-mysecret' from the environment 'tough-falcons'.
			metaplay secrets delete tough-falcons user-mysecret
		`),
	}

	secretsCmd.AddCommand(cmd)
}

func (o *DeleteSecretOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *DeleteSecretOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve environment.
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironment)
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
