package cmd

import (
	"os"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var flagCredentialsType string
var flagOutput string

var environmentGetKubeConfigCmd = &cobra.Command{
	Use:   "get-kubeconfig",
	Short: "Get the Kubernetes KubeConfig for the target environment",
	Run:   runGetKubeConfigCmd,
}

func init() {
	environmentCmd.AddCommand(environmentGetKubeConfigCmd)
	environmentGetKubeConfigCmd.Flags().StringVarP(&flagCredentialsType, "type", "t", "", "Type of credentials handling in kubeconfig, static or dynamic")
	environmentGetKubeConfigCmd.Flags().StringVarP(&flagOutput, "output", "o", "", "Path of the output file where to write kubeconfig (written to stdout if not specified)")
}

func runGetKubeConfigCmd(cmd *cobra.Command, args []string) {
	// Ensure we have fresh tokens.
	tokenSet, err := auth.EnsureValidTokenSet()
	if err != nil {
		log.Error().Msgf("Failed to get credentials: %v", err)
		os.Exit(1)
	}

	// Resolve target environment.
	targetEnv, err := resolveTargetEnvironment(tokenSet)
	if err != nil {
		log.Error().Msgf("Failed to resolve environment: %v", err)
		os.Exit(1)
	}

	// Default to credentialsType==dynamic for human users, and credentialsType==static for machine users
	credentialsType := flagCredentialsType
	if credentialsType == "" {
		if isHumanUser := tokenSet.RefreshToken != ""; isHumanUser {
			credentialsType = "dynamic"
		} else {
			credentialsType = "static"
		}
	}

	// Generate kubeconfig
	var kubeconfigPayload *string
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

	// (Maybe) write the output to a file
	if flagOutput != "" {
		log.Debug().Msgf("Writing kubeconfig to file %s", flagOutput)
		err = os.WriteFile(flagOutput, []byte(*kubeconfigPayload), 0600)
		if err != nil {
			log.Error().Msgf("Failed to write kubeconfig to file: %s", err)
		}
		log.Info().Msgf("Wrote kubeconfig to %s", flagOutput)
	} else {
		log.Info().Msg(*kubeconfigPayload)
	}
}
