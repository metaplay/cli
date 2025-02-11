/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"os"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type getKubeConfigOpts struct {
	flagCredentialsType string
	flagOutput          string

	argEnvironment string
}

func init() {
	o := getKubeConfigOpts{}

	cmd := &cobra.Command{
		Use:   "kubeconfig ENVIRONMENT [flags]",
		Short: "Get the Kubernetes KubeConfig for the target environment",
		Run:   runCommand(&o),
		Long: trimIndent(`
			Get the Kubernetes KubeConfig for accessing the target environment's cluster.

			The KubeConfig can be generated with two different credential handling types:
			- dynamic: Uses the Metaplay CLI to fetch fresh credentials when needed (recommended for human users)
			- static: Embeds static credentials in the KubeConfig (recommended for CI/CD pipelines)

			If no type is specified, it defaults to:
			- dynamic for human users (logged in with refresh token)
			- static for machine users (logged in with access token only)

			The KubeConfig can be written to a file using the --output flag, or printed to stdout if not specified.
		`),
		Example: trimIndent(`
			# Get KubeConfig for environment tough-falcons with dynamic credentials
			metaplay get kubeconfig tough-falcons --type=dynamic

			# Get KubeConfig with static credentials and save to a file
			metaplay get kubeconfig tough-falcons --type=static --output=kubeconfig.yaml

			# Get KubeConfig with default credentials type (based on user type)
			metaplay get kubeconfig tough-falcons
		`),
	}
	getCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVarP(&o.flagCredentialsType, "type", "t", "", "Type of credentials handling in kubeconfig, static or dynamic")
	flags.StringVarP(&o.flagOutput, "output", "o", "", "Path of the output file where to write kubeconfig (written to stdout if not specified)")
}

func (o *getKubeConfigOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("too many arguments (%d) provided, expecting maximum of 1", len(args))
	}

	// Store target environment, if provided
	if len(args) > 0 {
		o.argEnvironment = args[0]
	}

	return nil
}

func (o *getKubeConfigOpts) Run(cmd *cobra.Command) error {
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

	// Default to credentialsType==dynamic for human users, and credentialsType==static for machine users
	credentialsType := o.flagCredentialsType
	if credentialsType == "" {
		if isHumanUser := tokenSet.RefreshToken != ""; isHumanUser {
			credentialsType = "dynamic"
		} else {
			credentialsType = "static"
		}
	}

	// Generate kubeconfig
	var kubeconfigPayload string
	switch credentialsType {
	case "dynamic":
		kubeconfigPayload, err = targetEnv.GetKubeConfigWithExecCredential()
	case "static":
		kubeconfigPayload, err = targetEnv.GetKubeConfigWithEmbeddedCredentials()
	default:
		log.Error().Msg("Invalid credentials type; must be either \"static\" or \"dynamic\"")
		os.Exit(1)
	}

	if err != nil {
		log.Error().Msgf("Failed to get environment k8s config: %v", err)
		os.Exit(1)
	}

	// Write the kubeconfig payload to a file or stdout.
	if o.flagOutput != "" {
		log.Debug().Msgf("Write kubeconfig to file %s", o.flagOutput)
		err = os.WriteFile(o.flagOutput, []byte(kubeconfigPayload), 0600)
		if err != nil {
			return fmt.Errorf("failed to write kubeconfig to file: %v", err)
		}
		log.Info().Msgf("Wrote kubeconfig to %s", o.flagOutput)
	} else {
		log.Info().Msg(kubeconfigPayload)
	}

	return nil
}
