package cmd

import (
	"os"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var environmentGetKubernetesExecCredentialCmd = &cobra.Command{
	Use:   "get-kubernetes-execcredential",
	Short: "[internal] Get kubernetes credentials in execcredential format (used from the generated kubeconfigs)",
	Run:   runGetKubernetesExecCredentialCmd,
}

func init() {
	environmentGetKubernetesExecCredentialCmd.Hidden = true
	environmentCmd.AddCommand(environmentGetKubernetesExecCredentialCmd)
}

func runGetKubernetesExecCredentialCmd(cmd *cobra.Command, args []string) {
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

	// Get the Kubernetes credentials in the execcredential format
	credential, err := targetEnv.GetKubeExecCredential()
	if err != nil {
		log.Error().Msgf("Failed to get environment k8s execcredential: %v", err)
		os.Exit(1)
	}

	log.Info().Msg(*credential)
}
