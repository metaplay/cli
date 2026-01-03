/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/errors"
)

type secretsCreateOpts struct {
	UsePositionalArgs

	argEnvironment    string
	argSecretName     string
	flagLiteralValues []string
	flagFileValues    []string
	flagOverwrite     bool

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
			and --from-file flags to provide the key-value pairs. Multiple key-value pairs can be specified with any
			combination of the flag. All the keys must be unique within a single secret.

			By default, the command fails if the secret already exists. Use the --overwrite flag to replace an
			existing secret with the new values. This is useful for scripting and automation scenarios where you
			want to ensure a secret has specific values regardless of whether it already exists.

			The game server supports a special syntax 'kube-secret://<secretName>#<secretKey>' to access Kubernetes
			secrets in the various runtime options, configurable from the Options.*.yaml files.

			{Arguments}

			Related commands:
			- 'metaplay secrets update ENVIRONMENT NAME ...' to update an existing user secret.
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

			# Create or replace a secret (useful for scripting)
			metaplay secrets create tough-falcons user-mysecret --from-literal=apikey=secret123 --overwrite
		`),
	}

	secretsCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringArrayVar(&o.flagLiteralValues, "from-literal", []string{}, "Provide a key-value pair entry using the literal value (e.g., username=foobar)")
	flags.StringArrayVar(&o.flagFileValues, "from-file", []string{}, "Provide a key-value pair entry with the value read from a file (e.g., secret=../secret.txt)")
	flags.BoolVar(&o.flagOverwrite, "overwrite", false, "Overwrite the secret if it already exists")
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
			return fmt.Errorf("failed to read secret from file '%s': %v", filePath, err)
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

	// Print secret info.
	log.Info().Msg("")
	log.Info().Msgf("Create secret:")
	log.Info().Msgf("  Target environment: %s", styles.RenderTechnical(envConfig.HumanID))
	log.Info().Msgf("  Secret name:        %s", styles.RenderTechnical(o.argSecretName))
	secretKeys := make([]string, 0, len(o.payloadKeyValuePairs))
	for key := range o.payloadKeyValuePairs {
		secretKeys = append(secretKeys, key)
	}
	log.Info().Msgf("  Secret keys:        %s", styles.RenderListTechnical(secretKeys))
	log.Info().Msg("")

	// Try to create the secret.
	err = targetEnv.CreateSecret(cmd.Context(), o.argSecretName, o.payloadKeyValuePairs)
	if err == nil {
		log.Info().Msgf("✅ Secret %s created", o.argSecretName)
		return nil
	}

	// Check if the error is "already exists".
	if !errors.IsAlreadyExists(err) {
		// Some other error (network, permission, invalid name, etc.)
		return err
	}

	// Secret already exists. Handle based on flags and interactive mode.
	if o.flagOverwrite {
		// --overwrite flag specified, update the secret.
		err = targetEnv.UpdateSecret(cmd.Context(), o.argSecretName, o.payloadKeyValuePairs)
		if err != nil {
			return err
		}
		log.Info().Msgf("✅ Secret %s updated (overwritten)", o.argSecretName)
		return nil
	}

	// No --overwrite flag. Check if we're in interactive mode.
	if tui.IsInteractiveMode() {
		// Prompt user for confirmation.
		log.Info().Msg(styles.RenderWarning("⚠️  Secret already exists"))
		log.Info().Msg("")
		confirmed, err := tui.DoConfirmQuestion(cmd.Context(), "Do you want to overwrite the existing secret?")
		if err != nil {
			return err
		}
		if !confirmed {
			log.Info().Msg(styles.RenderError("❌ Operation cancelled"))
			return nil
		}
		// User confirmed, update the secret.
		err = targetEnv.UpdateSecret(cmd.Context(), o.argSecretName, o.payloadKeyValuePairs)
		if err != nil {
			return err
		}
		log.Info().Msgf("✅ Secret %s updated (overwritten)", o.argSecretName)
		return nil
	}

	// Non-interactive mode without --overwrite flag.
	return fmt.Errorf("secret %s already exists. Use --overwrite to replace it", o.argSecretName)
}
