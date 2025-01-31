/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

const metaplayGameServerChartName = "metaplay-gameserver"
const metaplayGameServerPodLabelSelector = "app=metaplay-server"

// Deploy a game server to the target environment with specified docker image version.
type DeployServerOpts struct {
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
	o := DeployServerOpts{}

	cmd := &cobra.Command{
		Use:   "deploy-server ENVIRONMENT IMAGE_TAG [flags] [-- EXTRA_ARGS]",
		Short: "Deploy a server into the target environment",
		Run:   runCommand(&o),
		Long: trimIndent(`
			Deploy a game server into a cloud environment using the specified docker image version.

			As part of deploying the server, the deployed server status is checked with the
			equivalent of 'metaplay env check-server-status'. This provides helpful diagnostics
			in CI pipelines in case the server fails to deploy for some reason.

			Arguments:
			- ENVIRONMENT specifies the target environment into which the image is pushed.
			- IMAGE:TAG is the docker image name and tag to push into the target environment.
			- EXTRA_ARGS are passed directly to Helm.

			Related commands:
			- 'metaplay build docker-image ...' to build the docker image.
			- 'metaplay env check-server-status ...' to check the status of a deployed server.
			- 'metaplay env push-image ...' to push the built image to the environment.
			- 'metaplay env server-logs ...' to view logs from the deployed server.
			- 'metaplay env debug-server ...' to debug a running server pod.
		`),
		Example: trimIndent(`
			# Deploy server into environment tough-falcons with the docker image tag 364cff09.
			metaplay env deploy-server tough-falcons 364cff09

			# Pass extra arguments to Helm.
			metaplay env deploy-server tough-falcons 364cff09 -- --set-string config.image.pullPolicy=Always

			# Use Helm chart from the local disk.
			metaplay env deploy-server tough-falcons 364cff09 --local-chart-path=/path/to/metaplay-gamserver

			# Override the Helm chart repository and version.
			metaplay env deploy-server tough-falcons 364cff09 --helm-chart-repo=https://custom-repo.domain.com --helm-chart-version=0.7.0

			# Override the Helm release name of the deployment.
			metaplay env deploy-server tough-falcons 364cff09 --deployment-name=my-deployment
		`),
	}
	environmentCmd.AddCommand(cmd)

	flags := cmd.Flags()
	// flags.StringVarP(&o.flagImageTag, "image-tag", "t", "", "Docker image tag, eg, '364cff092af8646bd'")
	flags.StringVar(&o.flagDeploymentName, "deployment-name", "gameserver", "Name to use for the Helm deployment")
	flags.StringVar(&o.flagHelmChartLocalPath, "local-chart-path", "", "Path to a local version of the metaplay-gameserver chart (repository and version are ignored if this is set)")
	flags.StringVar(&o.flagHelmChartRepository, "helm-chart-repo", "", "Override for Helm chart repository to use for the metaplay-gameserver chart")
	flags.StringVar(&o.flagHelmChartVersion, "helm-chart-version", "", "Override for Helm chart version to use, eg, '0.7.0'")
	flags.StringVarP(&o.flagHelmValuesPath, "values", "f", "", "Override for path to the Helm values file, e.g., 'Backend/Deployments/develop-server.yaml'")
}

func (o *DeployServerOpts) Prepare(cmd *cobra.Command, args []string) error {
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

func (o *DeployServerOpts) Run(cmd *cobra.Command) error {
	// Ensure the user is logged in
	tokenSet, err := tui.RequireLoggedIn(cmd.Context())
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Deploy Server to Cloud"))
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

	// Get docker credentials.
	dockerCredentials, err := targetEnv.GetDockerCredentials(envDetails)
	if err != nil {
		return fmt.Errorf("failed to get docker credentials: %v", err)
	}
	log.Debug().Msgf("Got docker credentials: username=%s", dockerCredentials.Username)

	// Fetch the labels from the remote docker image.
	remoteImageName := fmt.Sprintf("%s:%s", envDetails.Deployment.EcrRepo, o.argImageTag)
	imageLabels, err := fetchRemoteDockerImageLabels(dockerCredentials, remoteImageName)
	if err != nil {
		return err
	}

	// Determine the Metaplay SDK version from the docker image metadata.
	sdkVersion, found := imageLabels["io.metaplay.sdk_version"]
	if !found {
		return fmt.Errorf("invalid docker image: required label 'io.metaplay.sdk_version' not found in the image metadata")
	}
	log.Debug().Msgf("Metaplay SDK version used by image: %s", sdkVersion)

	// Resolve Helm chart to use (local or remote).
	var helmChartPath string
	if o.flagHelmChartLocalPath != "" {
		// Use local Helm chart directly.
		helmChartPath = o.flagHelmChartLocalPath
	} else {
		// Determine the Helm chart repo and version to use.
		helmChartRepo := coalesceString(project.config.HelmChartRepository, o.flagHelmChartRepository, "https://charts.metaplay.dev")
		minChartVersion, _ := version.NewVersion("0.7.0")
		helmChartPath, err = helmutil.FetchBestMatchingHelmChart(helmChartRepo, metaplayGameServerChartName, minChartVersion, chartVersionConstraints)
		if err != nil {
			return err
		}
	}
	log.Debug().Msgf("Helm chart path: %s", helmChartPath)

	// Resolve Helm values file path relative to current directory.
	valuesFiles := project.getServerValuesFiles(envConfig)

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
	existingRelease, err := helmutil.GetExistingRelease(actionConfig, metaplayGameServerChartName)
	if err != nil {
		return err
	}

	// Default shard config based on environment type.
	// \todo Auto-detect these from the infrastructure.
	var shardConfig []map[string]interface{}
	if envConfig.Type == portalapi.EnvironmentTypeProduction || envConfig.Type == portalapi.EnvironmentTypeStaging {
		shardConfig = []map[string]interface{}{
			{
				"name":      "all",
				"singleton": true,
				"requests": map[string]interface{}{
					"cpu":    "1500m",
					"memory": "3000M",
				},
			},
		}
	} else {
		shardConfig = []map[string]interface{}{
			{
				"name":      "all",
				"singleton": true,
				"requests": map[string]interface{}{
					"cpu":    "250m",
					"memory": "500Mi",
				},
			},
		}
	}

	// Default Helm values. The user Helm values files are applied on top so
	// all these values can be overridden by the user.
	// \todo check for the existence of the runtime options files
	helmValues := map[string]interface{}{
		"environment":       envConfig.Slug, // \todo use full name? need to allow more chars in Helm chart
		"environmentFamily": envConfig.getEnvironmentFamily(),
		"config": map[string]interface{}{
			"files": []string{
				"./Config/Options.base.yaml",
				envConfig.getEnvironmentSpecificRuntimeOptionsFile(),
			},
		},
		"tenant": map[string]interface{}{
			"discoveryEnabled": true,
		},
		"sdk": map[string]interface{}{
			"version": sdkVersion,
		},
		"image": map[string]interface{}{
			"tag": o.argImageTag,
		},
		"shards": shardConfig,
	}

	// Install or upgrade the Helm chart.
	log.Info().Msgf("Image tag: %s", styles.RenderTechnical(o.argImageTag))
	log.Info().Msgf("Deployment name: %s", styles.RenderTechnical(o.flagDeploymentName))
	log.Info().Msgf("Helm chart path: %s", styles.RenderTechnical(helmChartPath))
	log.Info().Msgf("Helm values files: %s", styles.RenderTechnical(strings.Join(valuesFiles, ", ")))
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
	log.Info().Msg(styles.RenderSuccess("✅ Successfully deployed server"))

	// Validate the game server status.
	err = targetEnv.WaitForServerToBeReady(cmd.Context())
	if err != nil {
		return fmt.Errorf("deployed server failed to start: %v", err)
	}

	return nil
}

// fetchRemoteDockerImageLabels retrieves the labels of an image in a remote Docker registry.
func fetchRemoteDockerImageLabels(creds *envapi.DockerCredentials, imageRef string) (map[string]string, error) {
	// Create a registry authenticator using the provided credentials
	authenticator := authn.FromConfig(authn.AuthConfig{
		Username: creds.Username,
		Password: creds.Password,
	})

	// Parse the image reference (name + tag or digest)
	ref, err := name.ParseReference(imageRef, name.WithDefaultRegistry(creds.RegistryURL))
	if err != nil {
		return nil, fmt.Errorf("failed to parse docker image reference: %w", err)
	}

	// Retrieve the image manifest and associated metadata
	desc, err := remote.Get(ref, remote.WithAuth(authenticator))
	if err != nil {
		return nil, fmt.Errorf("failed to get image descriptor: %w", err)
	}

	// Fetch the image configuration blob
	img, err := desc.Image()
	if err != nil {
		return nil, fmt.Errorf("failed to get image from descriptor: %w", err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get image config file: %w", err)
	}

	// Return the labels from the configuration
	return cfg.Config.Labels, nil
}

// Return the first non-empty string in the provided arguments.
func coalesceString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
