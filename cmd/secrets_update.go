/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type secretsUpdateOpts struct {
	UsePositionalArgs

	argEnvironment    string
	argSecretName     string
	flagLiteralValues []string
	flagFileValues    []string
	flagRemoveKeys    []string

	addOrUpdateKeyValuePairs map[string][]byte
}

func init() {
	o := secretsUpdateOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgument(&o.argSecretName, "NAME", "Name of the secret, e.g., 'user-some-secret'.")

	cmd := &cobra.Command{
		Use:   "update ENVIRONMENT NAME [flags]",
		Short: "Update a user secret in the target environment",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Update an existing user secret in the target environment. This command allows you to
			add new entries, update existing entries, or remove entries from a secret.

			Use the --from-literal and --from-file flags to add or update key-value pairs.
			Use the --remove flag to remove entries by key.

			At least one of --from-literal, --from-file, or --remove must be specified.

			{Arguments}

			Related commands:
			- 'metaplay secrets create ENVIRONMENT NAME ...' to create a new user secret.
			- 'metaplay secrets delete ENVIRONMENT NAME ...' to delete a user secret.
			- 'metaplay secrets list ENVIRONMENT ...' to list all user secrets.
			- 'metaplay secrets show ENVIRONMENT NAME ...' to show the contents of a user secret.
		`),
		Example: renderExample(`
			# Update an existing entry's value
			metaplay secrets update nimbly user-mysecret --from-literal=password=newsecret

			# Add a new entry to an existing secret
			metaplay secrets update nimbly user-mysecret --from-literal=apikey=abc123

			# Add entry from file
			metaplay secrets update nimbly user-mysecret --from-file=certificate=./cert.pem

			# Remove an entry
			metaplay secrets update nimbly user-mysecret --remove=oldkey

			# Combine: update one entry and remove another
			metaplay secrets update nimbly user-mysecret --from-literal=password=new --remove=deprecated-key
		`),
	}

	secretsCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringArrayVar(&o.flagLiteralValues, "from-literal", []string{}, "Add or update an entry using a literal value (e.g., key=value)")
	flags.StringArrayVar(&o.flagFileValues, "from-file", []string{}, "Add or update an entry with the value read from a file (e.g., key=filepath)")
	flags.StringArrayVar(&o.flagRemoveKeys, "remove", []string{}, "Remove an entry by key")
}

func (o *secretsUpdateOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Initialize key-value map.
	o.addOrUpdateKeyValuePairs = map[string][]byte{}

	// Resolve literal payload key-value pairs.
	for _, pair := range o.flagLiteralValues {
		// Split the literal pair into key and value
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return fmt.Errorf("invalid --from-literal format: '%s'. Expected 'key=value'", pair)
		}

		// Check for duplicate keys
		if _, exists := o.addOrUpdateKeyValuePairs[key]; exists {
			return fmt.Errorf("duplicate key detected: %s. All keys must be unique", key)
		}

		// Insert into the map
		o.addOrUpdateKeyValuePairs[key] = []byte(value)
	}

	// Resolve file entries.
	for _, pair := range o.flagFileValues {
		// Split the literal pair into key and value
		key, filePath, ok := strings.Cut(pair, "=")
		if !ok {
			return fmt.Errorf("invalid --from-file format: '%s'. Expected 'key=filepath'", pair)
		}

		// Check for duplicate keys
		if _, exists := o.addOrUpdateKeyValuePairs[key]; exists {
			return fmt.Errorf("duplicate key detected: %s. All keys must be unique", key)
		}

		// Read the file content
		fileContent, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read secret from file '%s': %v", filePath, err)
		}

		// Insert into the map
		o.addOrUpdateKeyValuePairs[key] = fileContent
	}

	// Validate that at least one operation is specified.
	if len(o.addOrUpdateKeyValuePairs) == 0 && len(o.flagRemoveKeys) == 0 {
		return fmt.Errorf("at least one of --from-literal, --from-file, or --remove must be specified")
	}

	// Validate that no key appears in both add/update and remove.
	for _, removeKey := range o.flagRemoveKeys {
		if _, exists := o.addOrUpdateKeyValuePairs[removeKey]; exists {
			return fmt.Errorf("key '%s' cannot be both added/updated and removed in the same operation", removeKey)
		}
	}

	return nil
}

func (o *secretsUpdateOpts) Run(cmd *cobra.Command) error {
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

	// Get the existing secret to merge changes.
	existingSecret, err := targetEnv.GetSecret(cmd.Context(), o.argSecretName)
	if err != nil {
		return fmt.Errorf("failed to get existing secret: %w", err)
	}

	// Start with existing data.
	newData := make(map[string][]byte)
	maps.Copy(newData, existingSecret.Data)

	// Process removals first.
	removedKeys := []string{}
	for _, removeKey := range o.flagRemoveKeys {
		if _, exists := newData[removeKey]; exists {
			delete(newData, removeKey)
			removedKeys = append(removedKeys, removeKey)
		} else {
			log.Warn().Msgf("Key '%s' does not exist in secret, skipping removal", removeKey)
		}
	}

	// Apply add/update operations.
	addedKeys := []string{}
	updatedKeys := []string{}
	for key, value := range o.addOrUpdateKeyValuePairs {
		if _, exists := existingSecret.Data[key]; exists {
			updatedKeys = append(updatedKeys, key)
		} else {
			addedKeys = append(addedKeys, key)
		}
		newData[key] = value
	}

	// Warn if secret will be empty after update.
	if len(newData) == 0 {
		log.Warn().Msg("Warning: Secret will have no entries after this update")
	}

	// Print update info.
	log.Info().Msg("")
	log.Info().Msgf("Update secret:")
	log.Info().Msgf("  Target environment: %s", styles.RenderTechnical(envConfig.HumanID))
	log.Info().Msgf("  Secret name:        %s", styles.RenderTechnical(o.argSecretName))
	if len(addedKeys) > 0 {
		log.Info().Msgf("  Keys to add:        %s", styles.RenderListTechnical(addedKeys))
	}
	if len(updatedKeys) > 0 {
		log.Info().Msgf("  Keys to update:     %s", styles.RenderListTechnical(updatedKeys))
	}
	if len(removedKeys) > 0 {
		log.Info().Msgf("  Keys to remove:     %s", styles.RenderListTechnical(removedKeys))
	}
	log.Info().Msg("")

	// Update the secret.
	err = targetEnv.UpdateSecret(cmd.Context(), o.argSecretName, newData)
	if err != nil {
		return err
	}

	log.Info().Msgf("✅ Secret %s updated", o.argSecretName)
	return nil
}
