/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Remove botclient deployment from target environment.
type removeBotClientOpts struct {
	UsePositionalArgs

	argEnvironment string
}

func init() {
	o := removeBotClientOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")

	cmd := &cobra.Command{
		Use:     "botclient [ENVIRONMENT]",
		Aliases: []string{"bots", "botclients"},
		Short:   "Remove the BotClient deployment from the target environment",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Remove the BotClient deployment from the target environment.

			{Arguments}
		`),
		Example: renderExample(`
			# Remove botclient deployment from environment nimbly.
			metaplay remove botclient nimbly
		`),
	}

	removeCmd.AddCommand(cmd)
}

func (o *removeBotClientOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *removeBotClientOpts) Run(cmd *cobra.Command) error {
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
	if err != nil {
		return clierrors.Wrap(err, "Failed to get kubeconfig for environment")
	}
	log.Debug().Msgf("Resolved kubeconfig to access environment")

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(kubeconfigPayload, envConfig.GetKubernetesNamespace())
	if err != nil {
		return clierrors.Wrap(err, "Failed to initialize Helm config")
	}

	// Resolve all deployed game server Helm releases.
	helmReleases, err := helmutil.HelmListReleases(actionConfig, metaplayLoadTestChartName)
	if err != nil {
		return clierrors.Wrap(err, "Failed to list botclient Helm releases")
	}
	if len(helmReleases) == 0 {
		log.Info().Msgf("No existing botclient deployment found in this environment, nothing to do.")
		return nil
	}

	// Uninstall all Helm releases (multiple releases should not happen but are possible).
	for _, release := range helmReleases {
		log.Info().Msgf("Uninstall Helm release %s...", release.Name)

		err := helmutil.UninstallRelease(actionConfig, release)
		if err != nil {
			return clierrors.Wrapf(err, "Failed to uninstall Helm release %s", release.Name)
		}
	}

	log.Info().Msgf("Successfully uninstalled bots deployment")
	return nil
}
