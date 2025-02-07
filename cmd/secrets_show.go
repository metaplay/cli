/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type ShowSecretOpts struct {
	flagFormat string

	argEnvironment string
	argSecretName  string
}

func init() {
	o := ShowSecretOpts{}

	// \todo specify payload
	cmd := &cobra.Command{
		Use:   "show ENVIRONMENT NAME [flags]",
		Short: "[preview] Show a user secret in the target environment",
		Run:   runCommand(&o),
		Long: trimIndent(`
			PREVIEW: This command is in preview and subject to change!

			Show the contents of a single user secret.

			By default, a human-readable text format is used. When using in a script, use
			the --format=json to output JSON format.

			Related commands:
			- 'metaplay secrets create ENVIRONMENT NAME ...' to create a new user secret.
			- 'metaplay secrets delete ENVIRONMENT NAME ...' to delete a user secret.
			- 'metaplay secrets list ENVIRONMENT ...' to list all user secrets.
		`),
		Example: trimIndent(`
			# Show the contents of secret user-mysecret in environment tough-falcons.
			metaplay secrets show tough-falcons user-mysecret

			# Show the contents of secret in text format (default).
			metaplay secrets show tough-falcons user-mysecret --format=text

			# Show the contents of secret in JSON format.
			metaplay secrets show tough-falcons user-mysecret --format=json

			# Extract the value of the secret field named 'default' and decode the raw value of it.
			metaplay secrets show tough-falcons user-mysecret --format=json | jq -r .data.default | base64 -d
		`),
	}

	secretsCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagFormat, "format", "text", "Output format. Valid values are 'text' or 'json'. JSON format includes all Kubernetes metadata.")
}

func (o *ShowSecretOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("exactly two arguments must be provided, got %d", len(args))
	}

	// Validate format
	if o.flagFormat != "text" && o.flagFormat != "json" {
		return fmt.Errorf("invalid format %q, must be either 'text' or 'json'", o.flagFormat)
	}

	// Store arguments.
	o.argEnvironment = args[0]
	o.argSecretName = args[1]

	return nil
}

func (o *ShowSecretOpts) Run(cmd *cobra.Command) error {
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

	// Create the secret.
	secret, err := targetEnv.GetSecret(cmd.Context(), o.argSecretName)
	if err != nil {
		return err
	}

	if o.flagFormat == "json" {
		secretJson, err := json.MarshalIndent(secret, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal secrets as JSON: %v", err)
		}

		log.Info().Msgf("%s", string(secretJson))
	} else {
		logSecret(secret, true)
	}

	return nil
}
