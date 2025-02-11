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

// Uninstall bots deployment from target environment.
type removeBotClientsOpts struct {
	argEnvironment string
}

func init() {
	o := removeBotClientsOpts{}

	cmd := &cobra.Command{
		Use:     "botclients ENVIRONMENT [flags]",
		Aliases: []string{"bots"},
		Short:   "[preview] Remove the BotClient deployment from the target environment",
		Run:     runCommand(&o),
		Long: trimIndent(`
			PREVIEW: This command is in preview and subject to change! It also still lacks some
			key functionality.

			Remove the BotClient deployment from the target environment.
		`),
		Example: trimIndent(`
			# Remove botclients from environment tough-falcons.
			metaplay remove botclients tough-falcons
		`),
	}

	removeCmd.AddCommand(cmd)
}

func (o *removeBotClientsOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("exactly one argument must be provided, got %d", len(args))
	}

	o.argEnvironment = args[0]
	return nil
}

func (o *removeBotClientsOpts) Run(cmd *cobra.Command) error {
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
	actionConfig, err := helmutil.NewActionConfig(kubeconfigPayload, envConfig.GetKubernetesNamespace())
	if err != nil {
		log.Error().Msgf("Failed to initialize Helm config: %v", err)
		os.Exit(1)
	}

	// Resolve all deployed game server Helm releases.
	helmReleases, err := helmutil.HelmListReleases(actionConfig, metaplayLoadTestChartName)
	if len(helmReleases) == 0 {
		return fmt.Errorf("no existing bots deployment found")
	}

	// Uninstall all Helm releases (multiple releases should not happen but are possible).
	for _, release := range helmReleases {
		log.Info().Msgf("Uninstall Helm release %s...", release.Name)

		err := helmutil.UninstallRelease(actionConfig, release)
		if err != nil {
			return fmt.Errorf("failed to uninstall Helm relese %s: %w", release.Name, err)
		}
	}

	log.Info().Msgf("Successfully uninstalled bots deployment")
	return nil
}
