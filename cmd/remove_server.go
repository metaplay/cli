/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"fmt"
	"os"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Remove the Metaplay game server deployment from target environment.
type removeGameServerOpts struct {
	UsePositionalArgs

	argEnvironment string
}

func init() {
	o := removeGameServerOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")

	cmd := &cobra.Command{
		Use:     "server ENVIRONMENT",
		Aliases: []string{"game-server"},
		Short:   "Remove the game server deployment from the target environment",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Remove the game server deployment from the target environment.

			{Arguments}
		`),
		Example: renderExample(`
			# Remove game server deployment from environment tough-falcons.
			metaplay remove game-server tough-falcons
		`),
	}

	removeCmd.AddCommand(cmd)
}

func (o *removeGameServerOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *removeGameServerOpts) Run(cmd *cobra.Command) error {
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

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Remove Game Server"))
	log.Info().Msg("")

	// Resolve all deployed game server Helm releases.
	helmReleases, err := helmutil.HelmListReleases(actionConfig, metaplayGameServerChartName)
	if err != nil {
		return fmt.Errorf("failed to resolve existing Helm releases: %v", err)
	}

	// If no releases found, exit.
	if len(helmReleases) == 0 {
		log.Info().Msgf("No game server deployment found in environment, nothing to do.")
		os.Exit(0)
	}

	// Warn about multiple game server deployments (can happen if deploying manually or with old CLI).
	if len(helmReleases) > 1 {
		log.Warn().Msgf("Multiple game server deployments found in environment, removing them all.")
	}

	// Uninstall all Helm releases using task runner.
	taskRunner := tui.NewTaskRunner()

	for _, release := range helmReleases {
		taskRunner.AddTask(fmt.Sprintf("Uninstall Helm release %s", release.Name), func(output *tui.TaskOutput) error {
			output.SetHeaderLines([]string{
				fmt.Sprintf("Release status: %s", release.Info.Status),
			})
			err := helmutil.UninstallRelease(actionConfig, release)
			if err != nil {
				return fmt.Errorf("failed to uninstall Helm release %s: %v", release.Name, err)
			}
			return nil
		})
	}

	// Run the tasks.
	if err = taskRunner.Run(); err != nil {
		return err
	}

	log.Info().Msg("✅ Successfully removed game server deployments")
	return nil
}
