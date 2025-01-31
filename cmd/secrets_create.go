/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type CreateSecretOpts struct {
	flagLiteralValues []string
	flagFileValues    []string

	argEnvironment string
	argSecretName  string

	payloadKeyValuePairs map[string][]byte
}

func init() {
	o := CreateSecretOpts{}

	cmd := &cobra.Command{
		Use:   "create ENVIRONMENT NAME [flags]",
		Short: "[experimental] Create a user secret in the target environment",
		Run:   runCommand(&o),
		Long: trimIndent(`
			WARNING: This command is experimental and subject to change!

			Create a user secret in the target environment with the given name and payload.

			Secret name must start with 'user-'. This avoids conflicts with other secrets.

			Each secret consists of multiple entries, with each entry being a key-value pair. Use the --from-literal
			and --from-file flags to prove the key-value pairs. Multiple key-value pairs can be specified with any
			combination of the flag. All the keys must be unique within a single secret.

			The game server supports a special syntax 'kube-secret://<secretName>#<secretKey>' to access Kubernetes
			secrets in the various runtime options, configurable from the Options.*.yaml files.

			Related commands:
			- 'metaplay secrets delete ENVIRONMENT NAME ...' to delete a user secret.
			- 'metaplay secrets list ENVIRONMENT ...' to list all user secrets.
			- 'metaplay secrets show ENVIRONMENT NAME ...' to show the contents of a user secret.
		`),
		Example: trimIndent(`
			# Create a secret named 'user-mysecret' with two entries.
			metaplay secrets create my-environment user-mysecret --from-literal=username=foobar --from-literal=password=tops3cret

			# Create a secret with entry payload read from a file.
			metaplay secrets create my-environment user-mysecret --from-file=credentials.json=../../credentials-dev.json
		`),
	}

	secretsCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringArrayVar(&o.flagLiteralValues, "from-literal", []string{}, "Provide a key-value pair entry using the literal value (e.g., username=foobar)")
	flags.StringArrayVar(&o.flagFileValues, "from-file", []string{}, "Provide a key-value pair entry with the value read from a file (e.g., secret=../secret.txt)")
}

func (o *CreateSecretOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("exactly two arguments must be provided, got %d", len(args))
	}

	// Store arguments.
	o.argEnvironment = args[0]
	o.argSecretName = args[1]

	// Initialize key-value map.
	o.payloadKeyValuePairs = map[string][]byte{}

	// Resolve literal payload key-value pairs.
	for _, pair := range o.flagLiteralValues {
		// Split the literal pair into key and value
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("Invalid --from-literal format: '%s'. Expected 'key=value'.", pair)
		}
		key := parts[0]
		value := parts[1]

		// Check for duplicate keys
		if _, exists := o.payloadKeyValuePairs[key]; exists {
			return fmt.Errorf("duplicate key detected: %s. All keys must be unique", key)
		}

		// Insert into the map
		o.payloadKeyValuePairs[key] = []byte(value)
	}

	// Resolve file entries.
	for _, pair := range o.flagFileValues {
		// Split the literal pair into key and value
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid --from-file format: '%s'. Expected 'key=filepath'.", pair)
		}
		key := parts[0]
		filePath := parts[1]

		// Check for duplicate keys
		if _, exists := o.payloadKeyValuePairs[key]; exists {
			return fmt.Errorf("duplicate key detected: %s. All keys must be unique", key)
		}

		// Read the file content
		fileContent, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to secret from file '%s': %v", filePath, err)
		}

		// Insert into the map
		o.payloadKeyValuePairs[key] = fileContent
	}

	return nil
}

func (o *CreateSecretOpts) Run(cmd *cobra.Command) error {
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
	err = targetEnv.CreateSecret(cmd.Context(), o.argSecretName, o.payloadKeyValuePairs)
	if err != nil {
		return err
	}

	log.Info().Msgf("Secret %s created", o.argSecretName)
	return nil
}
