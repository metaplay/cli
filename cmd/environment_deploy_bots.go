/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

// \todo More configurability: number of replicas, number of bots, etc.
// \todo Add checks that the deployment/pods are running as expected (equivalent of check-server-status)

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
)

const metaplayLoadTestChartName = "metaplay-loadtest"

// const metaplayBotClientPodLabelSelector = "app=botclient"

// Deploy bots to the target environment with specified docker image version.
type DeployBotsOpts struct {
	flagDeploymentName      string
	flagHelmChartLocalPath  string
	flagHelmChartRepository string
	flagHelmChartVersion    string
	flagHelmValuesPath      string

	argEnvironment string
	argImageTag    string
	extraArgs      []string
}

func init() {
	o := DeployBotsOpts{}

	cmd := &cobra.Command{
		Use:   "deploy-bots ENVIRONMENT IMAGE_TAG [flags] [-- EXTRA_ARGS]",
		Short: "[experimental] Deploy load testing bots into the target environment",
		Run:   runCommand(&o),
		Long: trimIndent(`
			WARNING: This command is experimental and subject to change! It also still lacks some
			key functionality.

			Deploy bots into the target cloud environment using the specified docker image version.

			Related commands:
			- 'metaplay build docker-image ...' to build the docker image.
			- 'metaplay env check-server-status ...' to check the status of a deployed server.
			- 'metaplay env push-image ...' to push the built image to the environment.
			- 'metaplay env server-logs ...' to view logs from the deployed server.
			- 'metaplay env debug-server ...' to debug a running server pod.
		`),
		Example: trimIndent(`
			# Deploy bots into environment tough-falcons with the docker image tag 364cff09.
			metaplay env deploy-bots tough-falcons 364cff09
		`),
	}
	environmentCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagDeploymentName, "deployment-name", "loadtest", "Name to use for the Helm deployment") // \todo default value?
	flags.StringVar(&o.flagHelmChartLocalPath, "local-chart-path", "", "Path to a local version of the metaplay-loadtest chart (repository and version are ignored if this is set)")
	flags.StringVar(&o.flagHelmChartRepository, "helm-chart-repo", "", "Override for Helm chart repository to use for the metaplay-loadtest chart")
	flags.StringVar(&o.flagHelmChartVersion, "helm-chart-version", "", "Override for Helm chart version to use, eg, '0.4.2'")
	flags.StringVarP(&o.flagHelmValuesPath, "values", "f", "", "Override for path to the Helm values file, e.g., 'Backend/Deployments/develop-server.yaml'")
}

func (o *DeployBotsOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("at least two arguments must be provided, got %d", len(args))
	}

	o.argEnvironment = args[0]
	o.argImageTag = args[1]
	o.extraArgs = args[2:]

	// Validate image tag.
	if o.argImageTag == "" {
		log.Panic().Msgf("Positional argument IMAGE_TAG is empty")
	}
	if strings.Contains(o.argImageTag, ":") {
		return fmt.Errorf("IMAGE_TAG must contain only the tag (not the repository prefix), eg, '364cff092af8646bd'")
	}

	// Validate deployment name.
	if o.flagDeploymentName == "" {
		return fmt.Errorf("an empty Helm deployment name was given with '--deployment-name=<name>'")
	}

	return nil
}

func (o *DeployBotsOpts) Run(cmd *cobra.Command) error {
	// Ensure the user is logged in
	tokenSet, err := tui.RequireLoggedIn(cmd.Context())
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Deploy Bots to Cloud"))
	log.Info().Msg("")

	// Resolve project and environment.
	project, envConfig, err := resolveProjectAndEnvironment(o.argEnvironment)
	if err != nil {
		return err
	}

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
		helmChartVersion := project.config.ServerChartVersion
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
	log.Debug().Msg("Get environment details")
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return err
	}

	// Resolve path to Helm chart (local or remote).
	var helmChartPath string
	if o.flagHelmChartLocalPath != "" {
		// Use local Helm chart directly.
		helmChartPath = o.flagHelmChartLocalPath
	} else {
		// Determine the Helm chart repo and version to use.
		helmChartRepo := coalesceString(project.config.HelmChartRepository, o.flagHelmChartRepository, "https://charts.metaplay.dev")
		minChartVersion, _ := version.NewVersion("0.4.0")
		helmChartPath, err = helmutil.FetchBestMatchingHelmChart(helmChartRepo, metaplayLoadTestChartName, minChartVersion, chartVersionConstraints)
		if err != nil {
			return err
		}
	}
	log.Info().Msgf("Helm chart path: %s", styles.RenderTechnical(helmChartPath))

	// Resolve Helm values file path relative to current directory.
	valuesFiles := project.getBotsValuesFiles(envConfig)

	// Get kubeconfig to access the environment.
	kubeconfigPayload, err := targetEnv.GetKubeConfigWithEmbeddedCredentials()
	if err != nil {
		return err
	}
	log.Debug().Msgf("Resolved kubeconfig to access environment")

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(*kubeconfigPayload, envConfig.getKubernetesNamespace())
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
	// \todo fix the configurability & params values
	helmValues := map[string]interface{}{
		"botclients": map[string]interface{}{
			"targetPort":      9339,
			"targetEnableTls": true,
			"maxBotId":        100000,
			"botsPerPod":      1,
			// "botSpawnRate": 10,
			// "botSessionDuration: "00:00:20",
			"image": map[string]interface{}{
				"repository": envDetails.Deployment.EcrRepo,
				"tag":        o.argImageTag,
			},
			"targetHost": envDetails.Deployment.ServerHostname,
			"cdnBaseUrl": fmt.Sprintf("https://%s", envDetails.Deployment.CdnS3Fqdn),
		},
	}

	// Install or upgrade the Helm chart.
	log.Info().Msgf("Deployment name: %s", o.flagDeploymentName)
	log.Info().Msgf("Helm chart path: %s", helmChartPath)
	log.Info().Msgf("Helm values files: %s", valuesFiles)
	log.Info().Msgf("Image tag: %s", o.argImageTag)
	log.Info().Msg("")
	_, err = helmutil.HelmUpgradeInstall(
		actionConfig,
		existingRelease,
		envConfig.getKubernetesNamespace(),
		o.flagDeploymentName,
		helmChartPath,
		helmValues,
		valuesFiles,
		5*time.Minute)
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msgf(styles.RenderSuccess("✅ Successfully deployed bots"))

	// Validate the bots status.
	// log.Info().Msgf("Check bot status...")
	// err = targetEnv.WaitForServerToBeReady(cmd.Context())
	// if err != nil {
	// 	return fmt.Errorf("deployed server failed to start: %v", err)
	// }

	return nil
}
