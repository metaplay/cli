/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"os"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type getKubeConfigOpts struct {
	UsePositionalArgs

	argEnvironment      string
	argAuthProvider     string
	flagCredentialsType string
	flagOutput          string
}

func init() {
	o := getKubeConfigOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgumentOpt(&o.argAuthProvider, "AUTH_PROVIDER", "Name of the auth provider to use. Defaults to 'metaplay'.")

	cmd := &cobra.Command{
		Use:   "kubeconfig ENVIRONMENT [AUTH_PROVIDER] [flags]",
		Short: "Get the Kubernetes KubeConfig for the target environment",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Get the Kubernetes KubeConfig for accessing the target environment's cluster.

			The KubeConfig can be generated with two different credential handling types:
			- dynamic: Uses the Metaplay CLI to fetch fresh credentials when needed (recommended for human users)
			- static: Embeds static credentials in the KubeConfig (recommended for CI/CD pipelines)

			If no type is specified, it defaults to:
			- dynamic for human users (logged in with refresh token)
			- static for machine users (logged in with access token only)

			The KubeConfig can be written to a file using the --output flag, or printed to stdout if not specified.

			The default auth provider is 'metaplay'. If you have multiple auth providers configured in your
			'metaplay-project.yaml', you can specify the name of the provider you want to use with the
			argument AUTH_PROVIDER.

			{Arguments}
		`),
		Example: renderExample(`
			# Get KubeConfig for environment nimbly with dynamic credentials
			metaplay get kubeconfig nimbly --type=dynamic

			# Get KubeConfig with static credentials and save to a file
			metaplay get kubeconfig nimbly --type=static --output=kubeconfig.yaml

			# Get KubeConfig with default credentials type (based on user type)
			metaplay get kubeconfig nimbly

			# Get KubeConfig using a custom auth provider
			metaplay get kubeconfig nimbly my-auth-provider
		`),
	}
	getCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVarP(&o.flagCredentialsType, "type", "t", "", "Type of credentials handling in kubeconfig, static or dynamic")
	flags.StringVarP(&o.flagOutput, "output", "o", "", "Path of the output file where to write kubeconfig (written to stdout if not specified)")
}

func (o *getKubeConfigOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *getKubeConfigOpts) Run(cmd *cobra.Command) error {
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

	// Resolve auth provider.
	authProviderName := o.argAuthProvider
	if authProviderName == "" {
		authProviderName = "metaplay"
	}
	authProvider, err := getAuthProvider(project, authProviderName)
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
		// Fetch the userinfo for an email.
		var userinfo *auth.UserInfoResponse
		userinfo, err = auth.FetchUserInfo(authProvider, tokenSet)
		if err != nil {
			return err
		}

		kubeconfigPayload, err = targetEnv.GetKubeConfigWithExecCredential(userinfo.Email)
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
