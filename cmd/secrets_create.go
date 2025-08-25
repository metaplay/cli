/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type secretsCreateOpts struct {
	UsePositionalArgs

	argEnvironment    string
	argSecretName     string
	flagLiteralValues []string
	flagFileValues    []string

	payloadKeyValuePairs map[string][]byte
}

func init() {
	o := secretsCreateOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgumentOpt(&o.argSecretName, "NAME", "Name of the secret, e.g., 'user-some-secret'.")

	cmd := &cobra.Command{
		Use:   "create ENVIRONMENT NAME [flags]",
		Short: "Create a user secret in the target environment",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Create a user secret in the target environment with the given name and payload.

			Secret name must start with 'user-'. This avoids conflicts with other secrets.

			Each secret consists of multiple entries, with each entry being a key-value pair. Use the --from-literal
			and --from-file flags to prove the key-value pairs. Multiple key-value pairs can be specified with any
			combination of the flag. All the keys must be unique within a single secret.

			The game server supports a special syntax 'kube-secret://<secretName>#<secretKey>' to access Kubernetes
			secrets in the various runtime options, configurable from the Options.*.yaml files.

			{Arguments}

			Related commands:
			- 'metaplay secrets delete ENVIRONMENT NAME ...' to delete a user secret.
			- 'metaplay secrets list ENVIRONMENT ...' to list all user secrets.
			- 'metaplay secrets show ENVIRONMENT NAME ...' to show the contents of a user secret.
		`),
		Example: renderExample(`
			# Create a secret named 'user-mysecret' in environment 'tough-falcons' with two entries.
			# Accessible with URLs 'kube-secret://user-mysecret#username' and 'kube-secret://user-mysecret#password'
			metaplay secrets create tough-falcons user-mysecret --from-literal=username=foobar --from-literal=password=tops3cret

			# Create a secret with entry payload read from a file.
			# Accessible with URL 'kube-secret://user-mysecret#credentials'
			metaplay secrets create tough-falcons user-mysecret --from-file=credentials=../../credentials-dev.json
		`),
	}

	secretsCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringArrayVar(&o.flagLiteralValues, "from-literal", []string{}, "Provide a key-value pair entry using the literal value (e.g., username=foobar)")
	flags.StringArrayVar(&o.flagFileValues, "from-file", []string{}, "Provide a key-value pair entry with the value read from a file (e.g., secret=../secret.txt)")
}

func (o *secretsCreateOpts) Prepare(cmd *cobra.Command, args []string) error {
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

func (o *secretsCreateOpts) Run(cmd *cobra.Command) error {
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

	// Create the secret.
	err = targetEnv.CreateSecret(cmd.Context(), o.argSecretName, o.payloadKeyValuePairs)
	if err != nil {
		return err
	}

	log.Info().Msgf("Secret %s created", o.argSecretName)
	return nil
}
