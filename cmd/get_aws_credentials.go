/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type getAWSCredentialsOpts struct {
	flagFormat string

	argEnvironment string
}

func init() {
	o := getAWSCredentialsOpts{}

	cmd := &cobra.Command{
		Use:   "aws-credentials ENVIRONMENT [flags]",
		Short: "Get AWS credentials for the target environment",
		Long: trimIndent(`
			Get temporary AWS credentials for accessing resources in the target environment.
			These credentials can be used to authenticate AWS CLI commands or SDK calls.

			The credentials include:
			- AWS Access Key ID
			- AWS Secret Access Key
			- AWS Session Token (for temporary credentials)

			Two output formats are supported:
			- text: Human-readable format, suitable for reading and copying values
			- json: Machine-readable format, suitable for parsing and automation

			Arguments:
			- ENVIRONMENT specifies the target environment to get credentials for.

			Related commands:
			- 'metaplay get kubeconfig ...' to get Kubernetes configuration
			- 'metaplay get environment-info ...' to get environment details
		`),
		Example: trimIndent(`
			# Get credentials in human-readable text format (default)
			metaplay get aws-credentials tough-falcons

			# Get credentials in JSON format for scripting
			metaplay get aws-credentials tough-falcons --format json

			# Example of using the credentials with AWS CLI (bash):
			# eval $(metaplay get aws-credentials tough-falcons --format json | jq -r '
			#   "export AWS_ACCESS_KEY_ID=\(.AccessKeyID)
			#    export AWS_SECRET_ACCESS_KEY=\(.SecretAccessKey)
			#    export AWS_SESSION_TOKEN=\(.SessionToken)"
			# ')
		`),
		Run: runCommand(&o),
	}
	getCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVarP(&o.flagFormat, "format", "f", "text", "Output format (text or json)")
}

func (o *getAWSCredentialsOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("expecting argument ENVIRONMENT, got %d", len(args))
	}

	if o.flagFormat != "text" && o.flagFormat != "json" {
		return fmt.Errorf("invalid format %q; must be either \"text\" or \"json\"", o.flagFormat)
	}

	o.argEnvironment = args[0]
	return nil
}

func (o *getAWSCredentialsOpts) Run(cmd *cobra.Command) error {
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

	// Create environment helper.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Get AWS credentials
	credentials, err := targetEnv.GetAWSCredentials()
	if err != nil {
		log.Error().Msgf("Failed to get AWS credentials: %v", err)
		os.Exit(1)
	}

	// Output the credentials in the requested format
	switch o.flagFormat {
	case "json":
		output, err := json.MarshalIndent(credentials, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal credentials to JSON: %v", err)
		}
		fmt.Println(string(output))
	case "text":
		fmt.Printf("AWS Access Key ID:     %s\n", credentials.AccessKeyID)
		fmt.Printf("AWS Secret Access Key: %s\n", credentials.SecretAccessKey)
		if credentials.SessionToken != "" {
			fmt.Printf("AWS Session Token:     %s\n", credentials.SessionToken)
		}
	}

	return nil
}
