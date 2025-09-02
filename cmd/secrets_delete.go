/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type secretsDeleteOpts struct {
	UsePositionalArgs

	argEnvironment string
	argSecretName  string
}

func init() {
	o := secretsDeleteOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgument(&o.argSecretName, "NAME", "Name of the secret, e.g., 'user-some-secret'.")

	cmd := &cobra.Command{
		Use:   "delete ENVIRONMENT NAME [flags]",
		Short: "Delete a user secret in the target environment",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
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

func (o *secretsDeleteOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *secretsDeleteOpts) Run(cmd *cobra.Command) error {
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

	// Print secret info.
	log.Info().Msg("")
	log.Info().Msgf("Delete secret:")
	log.Info().Msgf("  Target environment: %s", styles.RenderTechnical(envConfig.HumanID))
	log.Info().Msgf("  Secret name:        %s", styles.RenderTechnical(o.argSecretName))
	log.Info().Msg("")

	// Delete the secret.
	err = targetEnv.DeleteSecret(cmd.Context(), o.argSecretName)
	if err != nil {
		return err
	}

	log.Info().Msgf("✅ Secret %s deleted", o.argSecretName)
	return nil
}
