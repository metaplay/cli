package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/release"
)

const metaplayGameServerChartName = "metaplay-gameserver"
const metaplayGameServerPodLabelSelector = "app=metaplay-server"

var flagDeploymentName string
var flagHelmChartLocalPath string
var flagHelmChartRepository string
var flagHelmChartVersion string
var flagHelmValuesPath string

// environmentDeployServerCmd deploys a game server image to the target environment.
var environmentDeployServerCmd = &cobra.Command{
	Use:   "deploy-server",
	Short: "Deploy a server into the target environment",
	Run:   runDeployServerCmd,
}

func init() {
	environmentCmd.AddCommand(environmentDeployServerCmd)

	environmentDeployServerCmd.Flags().StringVarP(&flagImageTag, "image-tag", "t", "", "Docker image tag, eg, '364cff092af8646bd'")
	environmentDeployServerCmd.Flags().StringVar(&flagDeploymentName, "deployment-name", "gameserver", "Name to use for the Helm deployment")
	environmentDeployServerCmd.Flags().StringVar(&flagHelmChartLocalPath, "local-chart-path", "", "Path to a local version of the metaplay-gameserver chart (repository and version are ignored if this is set)")
	environmentDeployServerCmd.Flags().StringVar(&flagHelmChartRepository, "helm-chart-repo", "", "Override for Helm chart repository to use for the metaplay-gameserver chart")
	environmentDeployServerCmd.Flags().StringVar(&flagHelmChartVersion, "helm-chart-version", "", "Override for Helm chart version to use, eg, '0.7.0'")
	environmentDeployServerCmd.Flags().StringVarP(&flagHelmValuesPath, "values", "f", "", "Override for path to the Helm values file, e.g., 'Backend/Deployments/develop-server.yaml'")

	environmentDeployServerCmd.MarkFlagRequired("image-tag")
}

func runDeployServerCmd(cmd *cobra.Command, args []string) {
	// Load project config.
	projectDir, projectConfig, err := resolveProjectConfig()
	if err != nil {
		log.Error().Msgf("Failed to find project: %v", err)
		os.Exit(1)
	}

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

	// Find the environment's config.
	envConfig, err := findEnvironmentConfig(projectConfig, targetEnv.HumanId)
	if err != nil {
		log.Error().Msgf("Failed to resolve environment config: %v", err)
		os.Exit(1)
	}
	log.Info().Msgf("Use Helm values file: %s", envConfig.ValuesFile)

	// Validate image tag:
	if flagImageTag == "" {
		log.Error().Msg("Image tag must be provided with '--image-tag=<tag>'")
		os.Exit(2)
	}
	if strings.Contains(flagImageTag, ":") {
		log.Error().Msg("Image tag must contain only the tag (not the repository prefix), eg, '--image-tag=364cff092af8646bd'")
		os.Exit(2)
	}

	// Validate deployment name.
	if flagDeploymentName == "" {
		log.Error().Msg("A non-empty Helm deployment name must be given with '--deployment-name=<name>'")
		os.Exit(2)
	}

	// Validate Helm chart reference.
	var chartVersionConstraints version.Constraints = nil
	if flagHelmChartLocalPath != "" {
		// \todo check that path is valid
		err = helmutil.ValidateLocalHelmChart(flagHelmChartLocalPath)
		if err != nil {
			log.Error().Msgf("Invalid --helm-chart-path: %v", err)
			os.Exit(2)
		}
	} else {
		// Resolve Helm chart version to use, either from config file or command line override
		helmChartVersion := projectConfig.HelmChartVersion
		if flagHelmChartVersion != "" {
			helmChartVersion = flagHelmChartVersion
		}

		if helmChartVersion == "" {
			// \todo scan for latest version from registry?
			log.Error().Msg("Helm chart version must be provided with '--helm-chart-version=<version>'")
			os.Exit(2)
		}

		if helmChartVersion == "latest-prerelease" {
			// Accept any version
		} else {
			// Parse Helm chart semver range.
			chartVersionConstraints, err = version.NewConstraint(helmChartVersion)
			if err != nil {
				log.Error().Msgf("Invalid Helm chart version: %v", err)
				os.Exit(2)
			}
			log.Debug().Msgf("Accepted Helm chart semver constraints: %v", chartVersionConstraints)
		}
	}

	// Get environment details.
	log.Debug().Msg("Get environment details")
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		log.Error().Msgf("failed to get environment details: %v", err)
		os.Exit(1)
	}

	// Get docker credentials.
	dockerCredentials, err := targetEnv.GetDockerCredentials(envDetails)
	if err != nil {
		log.Error().Msgf("Failed to get docker credentials: %v", err)
		os.Exit(1)
	}
	log.Debug().Msgf("Got docker credentials: username=%s", dockerCredentials.Username)

	// \todo Check that the image tag exists in the remote repository
	// \todo Fetch the SDK version from the docker image label 'io.metaplay.sdk_version' for compat checks?

	// Note: CLIv2 does not care about the version labels in the Docker image as it is a problematic way
	//       of handling the versioning.

	// Fetch recent Helm chart versions (pre-0.5.0 is considered legacy).
	helmChartRepo := coalesceString(
		coalesceString(projectConfig.HelmChartRepository, flagHelmChartRepository),
		"https://charts.metaplay.dev")
	helmChartRepo = strings.TrimSuffix(helmChartRepo, "/")
	minChartVersion, _ := version.NewVersion("0.5.0")
	availableChartVersions, err := helmutil.FetchHelmChartVersions(helmChartRepo, metaplayGameServerChartName, minChartVersion)
	if err != nil {
		log.Error().Msgf("Failed to fetch Helm chart versions from the repository: %v", err)
		os.Exit(1)
	}
	log.Debug().Msgf("Available Helm chart versions: %v", availableChartVersions)

	// Find the best version match that is the latest one from the versions satisfying the requested version(s).
	useChartVersion, err := helmutil.ResolveBestMatchingVersion(availableChartVersions, chartVersionConstraints)
	if err != nil {
		log.Error().Msgf("Failed to find a matching Helm chart version: %v", err)
		os.Exit(1)
	}
	log.Info().Msgf("Use Helm chart version %s", useChartVersion)

	// Get kubeconfig to access the environment.
	kubeconfigPayload, err := targetEnv.GetKubeConfigWithEmbeddedCredentials()
	log.Debug().Msgf("Resolved kubeconfig to access environment")

	// Resolve path to Helm chart (local or remote).
	helmChartPath := flagHelmChartLocalPath
	if helmChartPath == "" {
		helmChartPath = fmt.Sprintf("%s/metaplay-gameserver-%s.tgz", helmChartRepo, useChartVersion)
	}
	log.Debug().Msgf("Helm chart path: %s", helmChartPath)

	// Resolve Helm values file path relative to current directory.
	// \todo support overriding values file?
	valuesFilePath := filepath.Join(projectDir, envConfig.ValuesFile)

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(*kubeconfigPayload, envDetails.Deployment.KubernetesNamespace)
	if err != nil {
		log.Error().Msgf("Failed to initialize Helm config: %v", err)
		os.Exit(1)
	}

	// Determine if there's a previous install
	releases, err := helmutil.HelmListReleases(actionConfig, metaplayGameServerChartName)
	if err != nil {
		log.Error().Msgf("Failed to resolve existing game server deployments: %v", err)
		os.Exit(1)
	}
	var existingRelease *release.Release
	if len(releases) == 0 {
		log.Info().Msgf("Deploy new Helm release %s...", flagDeploymentName)
	} else if len(releases) == 1 {
		existingRelease = releases[0]
		if existingRelease.Name != flagDeploymentName {
			log.Error().Msgf("Mismatched Helm release name: existing release is '%s', trying to install with name '%s'", existingRelease.Name, flagDeploymentName)
			os.Exit(1)
		}
		log.Info().Msgf("Upgrade existing Helm release %s...", existingRelease.Name)
	} else if len(releases) > 1 {
		log.Error().Msgf("Multiple game server releases deployed! Remove them using the 'metaplay environment uninstall-server' command.")
		os.Exit(1)
	}

	// Install or upgrade the Helm chart.
	// \todo need to rebase the envConfig.ValuesFile to current working dir!
	log.Debug().Msgf("Deployment name: %s", flagDeploymentName)
	log.Debug().Msgf("Helm chart path: %s", helmChartPath)
	log.Debug().Msgf("Values file path: %s", valuesFilePath)
	log.Debug().Msgf("Image tag: %s", flagImageTag)
	_, err = helmutil.HelmUpgradeInstall(
		actionConfig,
		existingRelease,
		envDetails.Deployment.KubernetesNamespace,
		flagDeploymentName,
		helmChartPath,
		valuesFilePath,
		flagImageTag,
		5*time.Minute)
	if err != nil {
		log.Fatal().Msgf("Failed to upgrade/install Helm chart: %v", err)
	}
	log.Info().Msgf("Successfully deployed server")

	// Validate the game server status.
	err = targetEnv.WaitForServerToBeReady()
	if err != nil {
		log.Error().Msgf("Game server did not start as expected: %v", err)
		os.Exit(1)
	}
}

func coalesceString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
