/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"os"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Remove the Metaplay game server deployment from target environment.
type removeGameServerOpts struct {
	argEnvironment string
}

func init() {
	o := removeGameServerOpts{}

	cmd := &cobra.Command{
		Use:   "remove-game-server ENVIRONMENT [flags]",
		Short: "Remove the game server deployment from the target environment",
		Run:   runCommand(&o),
	}

	deployCmd.AddCommand(cmd)
}

func (o *removeGameServerOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("exactly one argument must be provided, got %d", len(args))
	}

	o.argEnvironment = args[0]
	return nil
}

func (o *removeGameServerOpts) Run(cmd *cobra.Command) error {
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

	// Get kubeconfig to access the environment.
	kubeconfigPayload, err := targetEnv.GetKubeConfigWithEmbeddedCredentials()
	log.Debug().Msgf("Resolved kubeconfig to access environment")

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(*kubeconfigPayload, envConfig.getKubernetesNamespace())
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
		log.Info().Msgf("Remove release %s...", release.Name)

		err := helmutil.UninstallRelease(actionConfig, release)
		if err != nil {
			log.Error().Msgf("Failed to uninstall Helm release %s: %v", release.Name, err)
			os.Exit(1)
		}
	}

	log.Info().Msgf("Successfully removed game server deployment")
	return nil
}
