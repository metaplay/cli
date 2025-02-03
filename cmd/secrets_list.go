/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
)

type ListSecretsOpts struct {
	flagShowValues bool
	flagJsonOutput bool

	argEnvironment string
	argSecretName  string
}

func init() {
	o := ListSecretsOpts{}

	cmd := &cobra.Command{
		Use:   "list ENVIRONMENT [flags]",
		Short: "[experimental] List the user secrets in the target environment",
		Run:   runCommand(&o),
		Long: trimIndent(`
			WARNING: This command is experimental and subject to change!

			Show all user-created secrets in the target environment.

			In the default output mode, the secrets are sanitized to avoid accidentally showing
			them. Use --show-values flag to show the secrets. When using --json, the secret values
			are always shown.

			Related commands:
			- 'metaplay secrets create ENVIRONMENT NAME ...' to create a new user secret.
			- 'metaplay secrets delete ENVIRONMENT NAME ...' to delete a user secret.
			- 'metaplay secrets show ENVIRONMENT NAME ...' to show the contents of a user secret.
		`),
		Example: trimIndent(`
			# Show all secrets with their values censored.
			metaplay secrets list tough-falcons

			# Show all secrets with their values shown.
			metaplay secrets list tough-falcons --show-values

			# Show all secrets in JSON format (with all Kubernetes metadata included).
			metaplay secrets list tough-falcons --json
		`),
	}

	secretsCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.BoolVar(&o.flagShowValues, "show-values", false, "Show the values of the secrets. Only applies to default format.")
	flags.BoolVar(&o.flagJsonOutput, "json", false, "Output values as JSON (with all Kubernetes metadata included). Values are always shown!")
}

func (o *ListSecretsOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("exactly one argument must be provided, got %d", len(args))
	}

	// Store arguments.
	o.argEnvironment = args[0]

	return nil
}

func (o *ListSecretsOpts) Run(cmd *cobra.Command) error {
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

	// List the secret.
	secrets, err := targetEnv.ListSecrets(cmd.Context())
	if err != nil {
		return err
	}

	// Output the secrets in desired format.
	if o.flagJsonOutput {
		secretsJson, err := json.MarshalIndent(secrets, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal secrets as JSON: %v", err)
		}

		log.Info().Msgf("%s", string(secretsJson))
	} else {
		if len(secrets) == 0 {
			log.Info().Msgf("No secrets found in the environment")
		} else {
			// Write each secret to output
			for ndx, secret := range secrets {
				// Print separator.
				if ndx != 0 {
					log.Info().Msgf("---")
				}

				// Print secret.
				logSecret(&secret, o.flagShowValues)
			}
		}
	}

	return nil
}

// formatAge converts a duration to a human-readable AGE string
func formatAge(duration time.Duration) string {
	seconds := int(duration.Seconds())

	if seconds < 1 {
		// Less than 1 second
		return "just now"
	} else if seconds < 120 {
		// Up to 2 minutes
		return fmt.Sprintf("%ds", seconds)
	} else if seconds < 7200 {
		// Up to 2 hours
		minutes := seconds / 60
		return fmt.Sprintf("%dm", minutes)
	} else if seconds < 172800 {
		// Up to 2 days
		hours := seconds / 3600
		return fmt.Sprintf("%dh", hours)
	} else if seconds < 5184000 {
		// Up to 60 days (2 months)
		days := seconds / 86400
		return fmt.Sprintf("%dd", days)
	} else if seconds < 63072000 {
		// Up to 24 months (2 years)
		months := seconds / 2592000
		return fmt.Sprintf("%dmo", months)
	} else {
		// More than 2 years
		years := seconds / 31536000
		return fmt.Sprintf("%dy", years)
	}
}

func logSecret(secret *corev1.Secret, showValues bool) {
	age := time.Now().Sub(secret.CreationTimestamp.Time)
	log.Info().Msgf("Name: %s", secret.Name)
	log.Info().Msgf("Age: %s", formatAge(age))
	log.Info().Msgf("Data:")
	for key, value := range secret.Data {
		// Censor values unless they're requested to be shown.
		if !showValues {
			value = []byte("*****")
		}
		log.Info().Msgf("  %s: %s", key, value)
	}
}
