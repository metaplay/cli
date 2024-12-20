package cmd

import (
	"os"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// environmentUninstallServerCmd uninstalls the Metaplay game server deployment from target environment.
var environmentUninstallServerCmd = &cobra.Command{
	Use:   "uninstall-server",
	Short: "Uninstall the server from the target environment",
	Run:   runUninstallServerCmd,
}

func init() {
	environmentCmd.AddCommand(environmentUninstallServerCmd)
}

func runUninstallServerCmd(cmd *cobra.Command, args []string) {
	// Load project config.
	// _, projectConfig, err := resolveProjectConfig()
	// if err != nil {
	// 	log.Error().Msgf("Failed to find project: %v", err)
	// 	os.Exit(1)
	// }

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

	// Get environment details.
	log.Debug().Msg("Get environment details")
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		log.Error().Msgf("failed to get environment details: %v", err)
		os.Exit(1)
	}

	// Get kubeconfig to access the environment.
	kubeconfigPayload, err := targetEnv.GetKubeConfigWithEmbeddedCredentials()
	log.Debug().Msgf("Resolved kubeconfig to access environment")

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(*kubeconfigPayload, envDetails.Deployment.KubernetesNamespace)
	if err != nil {
		log.Error().Msgf("Failed to initialize Helm config: %v", err)
		os.Exit(1)
	}

	// Resolve all deployed game server Helm releases.
	helmReleases, err := helmutil.HelmListReleases(actionConfig, metaplayGameServerChartName)
	if len(helmReleases) == 0 {
		log.Error().Msgf("No game server deployment found")
		os.Exit(0)
	}

	// Uninstall all Helm releases (multiple releases should not happen but are possible).
	for _, release := range helmReleases {
		log.Info().Msgf("Uninstalling release %s...", release.Name)

		err := helmutil.UninstallRelease(actionConfig, release)
		if err != nil {
			log.Error().Msgf("Failed to uninstall Helm release %s: %v", release.Name, err)
			os.Exit(1)
		}
	}

	log.Info().Msgf("Successfully uninstalled server")
}
