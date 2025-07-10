/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

// \todo More configurability: number of replicas, number of bots, etc.
// \todo Add checks that the deployment/pods are running as expected

import (
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/release"
)

const metaplayLoadTestChartName = "metaplay-loadtest"

// const metaplayBotClientPodLabelSelector = "app=botclient"

// Deploy bots to the target environment with specified docker image version.
type deployBotClientOpts struct {
	UsePositionalArgs

	argEnvironment          string
	argImageTag             string
	extraArgs               []string
	flagHelmReleaseName     string
	flagHelmChartLocalPath  string
	flagHelmChartRepository string
	flagHelmChartVersion    string
	flagHelmValuesPath      string
}

func init() {
	o := deployBotClientOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgument(&o.argImageTag, "IMAGE_TAG", "Docker image name and tag, eg, '364cff09'.")
	args.SetExtraArgs(&o.extraArgs, "Passed as-is to Helm.")

	cmd := &cobra.Command{
		Use:     "botclient [ENVIRONMENT] [IMAGE_TAG] [flags] [-- EXTRA_ARGS]",
		Aliases: []string{"bots", "botclients"},
		Short:   "[preview] Deploy load testing bots into the target environment",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change! It also still lacks some
			key functionality.

			Deploy bots into the target cloud environment using the specified docker image version.
			The image must exist in the target environment image repository.

			{Arguments}

			Related commands:
			- 'metaplay build image ...' to build the docker image.
			- 'metaplay image push ...' to push the built image to the environment.
			- 'metaplay debug logs ...' to view logs from the deployed server.
			- 'metaplay debug shell ...' to debug a running server pod.
		`),
		Example: renderExample(`
			# Deploy bots into environment tough-falcons with the docker image tag 364cff09.
			metaplay deploy botclient tough-falcons 364cff09
		`),
	}
	deployCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagHelmReleaseName, "helm-release-name", "", "Helm release name to use for the bot deployment (defaults to '<environmentID>-loadtest'")
	flags.StringVar(&o.flagHelmChartLocalPath, "local-chart-path", "", "Path to a local version of the metaplay-loadtest chart (repository and version are ignored if this is set)")
	flags.StringVar(&o.flagHelmChartRepository, "helm-chart-repo", "", "Override for Helm chart repository to use for the metaplay-loadtest chart")
	flags.StringVar(&o.flagHelmChartVersion, "helm-chart-version", "", "Override for Helm chart version to use, eg, '0.4.2'")
	flags.StringVarP(&o.flagHelmValuesPath, "values", "f", "", "Override for path to the Helm values file, e.g., 'Backend/Deployments/develop-botclients.yaml'")
}

func (o *deployBotClientOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate image tag.
	if o.argImageTag == "" {
		log.Panic().Msgf("Positional argument IMAGE_TAG is empty")
	}
	if strings.Contains(o.argImageTag, ":") {
		return fmt.Errorf("IMAGE_TAG must contain only the tag (not the repository prefix), eg, '364cff092af8646bd'")
	}

	return nil
}

func (o *deployBotClientOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	// Resolve project and environment.
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironment)
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Deploy Bots to Cloud"))
	log.Info().Msg("")

	// Create TargetEnvironment.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Validate Helm chart reference.
	var chartVersionConstraints version.Constraints = nil
	if o.flagHelmChartLocalPath != "" {
		err = helmutil.ValidateLocalHelmChart(o.flagHelmChartLocalPath)
		if err != nil {
			return fmt.Errorf("invalid --helm-chart-path: %v", err)
		}
	} else {
		// Resolve Helm chart version to use, either from config file or command line override
		helmChartVersion := project.Config.BotClientChartVersion
		if o.flagHelmChartVersion != "" {
			helmChartVersion = o.flagHelmChartVersion
		}

		if helmChartVersion == "latest-prerelease" {
			// Accept any version
		} else {
			// Parse Helm chart semver range.
			chartVersionConstraints, err = version.NewConstraint(helmChartVersion)
			if err != nil {
				return fmt.Errorf("invalid Helm chart version: %v", err)
			}
			log.Debug().Msgf("Accepted Helm chart semver constraints: %v", chartVersionConstraints)
		}
	}

	// Get environment details.
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return err
	}

	// Resolve path to Helm chart (local or remote).
	var helmChartPath string
	var useHelmChartVersion string
	if o.flagHelmChartLocalPath != "" {
		// Use local Helm chart directly.
		helmChartPath = o.flagHelmChartLocalPath
		useHelmChartVersion = "local"
	} else {
		// Determine the Helm chart repo and version to use.
		helmChartRepo := coalesceString(project.Config.HelmChartRepository, o.flagHelmChartRepository, "https://charts.metaplay.dev")
		minChartVersion, _ := version.NewVersion("0.4.0")
		useHelmChartVersion, err = helmutil.ResolveBestMatchingHelmVersion(helmChartRepo, metaplayLoadTestChartName, minChartVersion, chartVersionConstraints)
		helmChartPath = helmutil.GetHelmChartPath(helmChartRepo, metaplayLoadTestChartName, useHelmChartVersion)
		if err != nil {
			return err
		}
	}

	// Resolve Helm values file path relative to current directory.
	valuesFiles := project.GetBotClientValuesFiles(envConfig)

	// Get kubeconfig to access the environment.
	kubeconfigPayload, err := targetEnv.GetKubeConfigWithEmbeddedCredentials()
	if err != nil {
		return err
	}
	log.Debug().Msgf("Resolved kubeconfig to access environment")

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(kubeconfigPayload, envConfig.GetKubernetesNamespace())
	if err != nil {
		return fmt.Errorf("failed to initialize Helm config: %v", err)
	}

	// Determine if there's an existing release deployed.
	existingRelease, err := helmutil.GetExistingRelease(actionConfig, metaplayLoadTestChartName)
	if err != nil {
		return err
	}

	// Default Helm values. The user Helm values files are applied on top so
	// all these values can be overridden by the user.
	helmDefaultValues := map[string]any{
		"environmentFamily": "Development", // not really but shouldn't matter in botclient
		"botclients": map[string]any{
			"targetPort":         9339,
			"targetEnableTls":    true,
			"maxBotId":           1000,
			"botsPerPod":         10,
			"botSpawnRate":       5,
			"botSessionDuration": "00:00:20",
			"image": map[string]any{
				"repository": envDetails.Deployment.EcrRepo,
				"tag":        o.argImageTag,
			},
			"targetHost":       envDetails.Deployment.ServerHostname,
			"targetTlsEnabled": true,
			"cdnBaseUrl":       fmt.Sprintf("https://%s", envDetails.Deployment.CdnS3Fqdn),
		},
		"prometheus": map[string]any{
			"enabled": true,
			"port":    9090,
		},
		"resources": map[string]any{
			"limits": map[string]any{
				"memory": "1024Mi",
				"cpu":    1,
			},
			"requests": map[string]any{
				"memory": "128Mi",
				"cpu":    0.1,
			},
		},
	}
	helmRequiredValues := map[string]any{
		"botclients": map[string]any{
			"image": map[string]any{
				"repository": envDetails.Deployment.EcrRepo,
				"tag":        o.argImageTag,
			},
		},
	}

	// Resolve Helm release name. If not specified, default to:
	// - Earlier name if a deployment already exists.
	// - '<environmentID>-loadtest' otherwise.
	helmReleaseName := o.flagHelmReleaseName
	helmReleaseNameBadge := ""
	if helmReleaseName == "" {
		if existingRelease != nil {
			helmReleaseName = existingRelease.Name
			helmReleaseNameBadge = styles.RenderMuted("[update existing]")
		} else {
			helmReleaseName = fmt.Sprintf("%s-loadtest", envConfig.HumanID)
			helmReleaseNameBadge = styles.RenderMuted("[default]")
		}
	}

	// Show info.
	log.Info().Msg("Target environment:")
	log.Info().Msgf("  Name:               %s", styles.RenderTechnical(envConfig.Name))
	log.Info().Msgf("  ID:                 %s", styles.RenderTechnical(envConfig.HumanID))
	log.Info().Msgf("  Type:               %s", styles.RenderTechnical(string(envConfig.Type)))
	log.Info().Msg("Build information:")
	log.Info().Msgf("  Image tag:          %s", styles.RenderTechnical(o.argImageTag))
	log.Info().Msgf("Deployment info:")
	log.Info().Msgf("  Helm release name:  %s %s", styles.RenderTechnical(helmReleaseName), helmReleaseNameBadge)
	log.Info().Msgf("  Helm values files:  %s", styles.RenderTechnical(coalesceString(strings.Join(valuesFiles, ", "), "none")))
	log.Info().Msg("")

	// Check if the existing release is in some kind of pending state
	if existingRelease != nil {
		releaseName := existingRelease.Name
		releaseStatus := existingRelease.Info.Status
		if releaseStatus == release.StatusUninstalling {
			return fmt.Errorf("Helm release %s is in state 'uninstalling'; try again later or manually uninstall the botclient with 'metaplay remove botclient'", releaseName)
		} else if releaseStatus.IsPending() {
			return fmt.Errorf("Helm release %s is in state '%s'; you can manually uninstall the botclient with 'metaplay remove botclient'", releaseName, releaseStatus)
		}
		log.Debug().Msgf("Existing Helm release info: %+v", existingRelease.Info)
	}

	taskRunner := tui.NewTaskRunner()

	// Install or upgrade the Helm chart.
	taskRunner.AddTask("Deploy loadtest Helm chart", func(output *tui.TaskOutput) error {
		_, err = helmutil.HelmUpgradeOrInstall(
			output,
			actionConfig,
			existingRelease,
			envConfig.GetKubernetesNamespace(),
			helmReleaseName,
			helmChartPath,
			useHelmChartVersion,
			valuesFiles,
			helmDefaultValues,
			helmRequiredValues,
			5*time.Minute)
		return err
	})

	// Validate the bots status.
	// log.Info().Msgf("Check bot status...")
	// err = targetEnv.WaitForServerToBeReady(cmd.Context())
	// if err != nil {
	// 	return fmt.Errorf("deployed server failed to start: %v", err)
	// }

	// Run all tasks.
	if err = taskRunner.Run(); err != nil {
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ Successfully deployed bots"))

	return nil
}
