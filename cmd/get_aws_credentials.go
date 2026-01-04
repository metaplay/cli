/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"fmt"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type getAWSCredentialsOpts struct {
	UsePositionalArgs

	argEnvironment string
	flagFormat     string
}

func init() {
	o := getAWSCredentialsOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")

	cmd := &cobra.Command{
		Use:     "aws-credentials ENVIRONMENT [flags]",
		Aliases: []string{"aws-creds"},
		Short:   "Get AWS credentials for the target environment",
		Long: renderLong(&o, `
			Get temporary AWS credentials for accessing resources in the target environment.
			These credentials can be used to authenticate AWS CLI commands or SDK calls.

			The credentials include:
			- AWS Access Key ID
			- AWS Secret Access Key
			- AWS Session Token (for temporary credentials)

			Two output formats are supported:
			- text: Human-readable format, suitable for reading and copying values
			- json: Machine-readable format, suitable for parsing and automation

			{Arguments}

			Related commands:
			- 'metaplay get kubeconfig ...' to get Kubernetes configuration
			- 'metaplay get environment-info ...' to get environment details
		`),
		Example: renderExample(`
			# Get credentials in human-readable text format (default)
			metaplay get aws-credentials tough-falcons

			# Get credentials in JSON format for scripting
			metaplay get aws-credentials tough-falcons --format json

			# Example of using the credentials with AWS CLI (bash):
			eval $(metaplay get aws-credentials tough-falcons --format json | jq -r '
			  "export AWS_ACCESS_KEY_ID=\(.AccessKeyId)
			   export AWS_SECRET_ACCESS_KEY=\(.SecretAccessKey)
			   export AWS_SESSION_TOKEN=\(.SessionToken)"
			')
		`),
		Run: runCommand(&o),
	}
	getCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVarP(&o.flagFormat, "format", "f", "text", "Output format (text or json)")
}

func (o *getAWSCredentialsOpts) Prepare(cmd *cobra.Command, args []string) error {
	if o.flagFormat != "text" && o.flagFormat != "json" {
		return fmt.Errorf("invalid format %q; must be either \"text\" or \"json\"", o.flagFormat)
	}

	return nil
}

func (o *getAWSCredentialsOpts) Run(cmd *cobra.Command) error {
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

	// Create environment helper.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Get AWS credentials
	credentials, err := targetEnv.GetAWSCredentials()
	if err != nil {
		return clierrors.Wrap(err, "Failed to get AWS credentials")
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
		log.Info().Msgf("AWS Access Key ID:     %s", credentials.AccessKeyID)
		log.Info().Msgf("AWS Secret Access Key: %s", credentials.SecretAccessKey)
		if credentials.SessionToken != "" {
			log.Info().Msgf("AWS Session Token:     %s", credentials.SessionToken)
		}
	}

	return nil
}
