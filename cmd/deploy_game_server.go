/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
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
type deployGameServerOpts struct {
	flagHelmReleaseName     string
	flagHelmChartLocalPath  string
	flagHelmChartRepository string
	flagHelmChartVersion    string
	flagHelmValuesPath      string

	argEnvironment  string
	argImageNameTag string
	extraArgs       []string
}

func init() {
	o := deployGameServerOpts{}

	cmd := &cobra.Command{
		Use:   "game-server ENVIRONMENT [IMAGE:]TAG [flags] [-- EXTRA_ARGS]",
		Short: "Deploy a game server into the target environment",
		Run:   runCommand(&o),
		Long: trimIndent(`
			Deploy a game server into a cloud environment using the specified docker image version.

			After deploying the server image, various checks are run against the deployment to help
			help diagnose any potential issues:
			- All expected pods are present, healthy, and ready.
			- Client-facing domain name resolves correctly.
			- Game server responds to client traffic.
			- Admin domain name resolves correctly.
			- Admin endpoint responds with a success code.

			When a full docker image tag is specified (eg, 'mygame:364cff09'), the image is first
			pushed to the environment's registry. If only a tag is specified (eg, '364cff09'), the
			image is assumed to be present in the remote registry already.

			Arguments:
			- ENVIRONMENT specifies the target environment into which the image is pushed.
			- [IMAGE:]TAG is the image to deploy; specify full local IMAGE:TAG to also push the image.
			- EXTRA_ARGS are passed directly to Helm.

			Related commands:
			- 'metaplay build docker-image ...' to build the docker image.
			- 'metaplay image push ...' to push the built image to the environment.
			- 'metaplay debug logs ...' to view logs from the deployed server.
			- 'metaplay debug run-shell ...' to start a shell on a running server pod.
		`),
		Example: trimIndent(`
			# Push the local image and deploy to the environment tough-falcons.
			metaplay deploy game-server tough-falcons mygame:364cff09

			# Deploy an image that has already been pushed into the environment.
			metaplay deploy game-server tough-falcons 364cff09

			# Pass extra arguments to Helm.
			metaplay deploy game-server tough-falcons mygame:364cff09 -- --set-string config.image.pullPolicy=Always

			# Use Helm chart from the local disk.
			metaplay deploy game-server tough-falcons mygame:364cff09 --local-chart-path=/path/to/metaplay-gameserver

			# Override the Helm chart repository and version.
			metaplay deploy game-server tough-falcons mygame:364cff09 --helm-chart-repo=https://custom-repo.domain.com --helm-chart-version=0.7.0

			# Override the Helm release name.
			metaplay deploy game-server tough-falcons mygame:364cff09 --helm-release-name=my-release-name
		`),
	}
	deployCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagHelmReleaseName, "helm-release-name", "gameserver", "Helm release name to use for the game server deployment")
	flags.StringVar(&o.flagHelmChartLocalPath, "local-chart-path", "", "Path to a local version of the metaplay-gameserver chart (repository and version are ignored if this is set)")
	flags.StringVar(&o.flagHelmChartRepository, "helm-chart-repo", "", "Override for Helm chart repository to use for the metaplay-gameserver chart")
	flags.StringVar(&o.flagHelmChartVersion, "helm-chart-version", "", "Override for Helm chart version to use, eg, '0.7.0'")
	flags.StringVarP(&o.flagHelmValuesPath, "values", "f", "", "Override for path to the Helm values file, e.g., 'Backend/Deployments/develop-server.yaml'")
}

func (o *deployGameServerOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("expecting two arguments: ENVIRONMENT and [IMAGE:]TAG")
	}

	o.argEnvironment = args[0]
	o.argImageNameTag = args[1]
	o.extraArgs = args[2:]

	// Validate image tag.
	if o.argImageNameTag == "" {
		return fmt.Errorf("received empty docker IMAGE tag argument")
	}

	// Validate Helm release name.
	if o.flagHelmReleaseName == "" {
		return fmt.Errorf("an empty Helm release name was given with '--helm-release-name=<name>'")
	}

	return nil
}

func (o *deployGameServerOpts) Run(cmd *cobra.Command) error {
	// Ensure the user is logged in
	tokenSet, err := tui.RequireLoggedIn(cmd.Context())
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Deploy Game Server to Cloud"))
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

	// Push the image to the remote repository (if full name is specified).
	useLocalImage := strings.Contains(o.argImageNameTag, ":")
	var imageTag string
	var imageConfig *v1.ConfigFile
	if useLocalImage {
		// Resolve metadata from local image.
		imageConfig, err = fetchLocalDockerImageMetadata(o.argImageNameTag)
		if err != nil {
			return err
		}

		// Extract the tag part.
		imageTag, err = extractDockerImageTag(o.argImageNameTag)
		if err != nil {
			return err
		}
	} else {
		imageTag = o.argImageNameTag

		// Fetch the labels from the remote docker image.
		remoteImageName := fmt.Sprintf("%s:%s", envDetails.Deployment.EcrRepo, imageTag)
		imageConfig, err = fetchRemoteDockerImageMetadata(dockerCredentials, remoteImageName)
		if err != nil {
			return err
		}
	}

	// Determine the Metaplay SDK version, commit id, and build number from the docker image metadata.
	imageLabels := imageConfig.Config.Labels
	imageSdkVersion, found := imageLabels["io.metaplay.sdk_version"]
	if !found {
		return fmt.Errorf("invalid docker image: required label 'io.metaplay.sdk_version' not found in the image metadata")
	}
	log.Debug().Msgf("Metaplay SDK version found in the image: %s", imageSdkVersion)

	imageCommitId, hasCommitId := imageLabels["io.metaplay.commit_id"]
	if !hasCommitId {
		return fmt.Errorf("invalid docker image: required label 'io.metaplay.commit_id' not found in the image metadata")
	}
	log.Debug().Msgf("Commit ID found in the image: %s", imageCommitId)

	imageBuildNumber, hasBuildNumber := imageLabels["io.metaplay.build_number"]
	if !hasBuildNumber {
		return fmt.Errorf("invalid docker image: required label 'io.metaplay.build_number' not found in the image metadata")
	}
	log.Debug().Msgf("Build number found in the image: %s", imageBuildNumber)

	// Resolve Helm chart to use (local or remote).
	var helmChartPath string
	var useHelmChartVersion string
	if o.flagHelmChartLocalPath != "" {
		// Use local Helm chart directly.
		helmChartPath = o.flagHelmChartLocalPath
		useHelmChartVersion = "local"
	} else {
		// Determine the Helm chart repo and version to use.
		helmChartRepo := coalesceString(project.config.HelmChartRepository, o.flagHelmChartRepository, "https://charts.metaplay.dev")
		minChartVersion, _ := version.NewVersion("0.7.0")
		useHelmChartVersion, err = helmutil.ResolveBestMatchingHelmVersion(helmChartRepo, metaplayGameServerChartName, minChartVersion, chartVersionConstraints)
		helmChartPath = helmutil.GetHelmChartPath(helmChartRepo, metaplayGameServerChartName, useHelmChartVersion)
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
		"environment":       envConfig.HumanID, // \todo use full name? need to allow more chars in Helm chart
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
			"version": imageSdkVersion,
		},
		"image": map[string]interface{}{
			"tag": imageTag,
		},
		"shards": shardConfig,
	}

	// Show info.
	log.Info().Msgf("Target environment:")
	log.Info().Msgf("  Name:               %s", styles.RenderTechnical(envConfig.Name))
	log.Info().Msgf("  ID:                 %s", styles.RenderTechnical(envConfig.HumanID))
	log.Info().Msgf("  Type:               %s", styles.RenderTechnical(string(envConfig.Type)))
	log.Info().Msgf("Build information:")
	log.Info().Msgf("  Build number:       %s", styles.RenderTechnical(imageBuildNumber))
	log.Info().Msgf("  Commit ID:          %s", styles.RenderTechnical(imageCommitId))
	log.Info().Msgf("  Created:            %s", styles.RenderTechnical(humanize.Time(imageConfig.Created.Time)))
	log.Info().Msgf("  Metaplay SDK:       %s", styles.RenderTechnical(imageSdkVersion))
	log.Info().Msgf("Deployment info:")
	if o.flagHelmChartLocalPath != "" {
		log.Info().Msgf("  Helm chart path:    %s", styles.RenderTechnical(helmChartPath))
	} else {
		log.Info().Msgf("  Helm chart version: %s", styles.RenderTechnical(useHelmChartVersion))
	}
	log.Info().Msgf("  Helm release name:  %s", styles.RenderTechnical(o.flagHelmReleaseName))
	if len(valuesFiles) > 0 {
		log.Info().Msgf("  Helm values files:  %s", styles.RenderTechnical(strings.Join(valuesFiles, ", ")))
	}
	// \todo list of runtime options files
	log.Info().Msg("")

	taskRunner := tui.NewTaskRunner()

	// If using local image, add task to push it.
	if useLocalImage {
		taskRunner.AddTask("Push docker image to environment repository", func() error {
			return pushDockerImage(cmd.Context(), o.argImageNameTag, envDetails.Deployment.EcrRepo, dockerCredentials)
		})
	}

	// Install or upgrade the Helm chart.
	taskRunner.AddTask("Deploy game server using Helm", func() error {
		_, err := helmutil.HelmUpgradeOrInstall(
			actionConfig,
			existingRelease,
			envConfig.getKubernetesNamespace(),
			o.flagHelmReleaseName,
			helmChartPath,
			helmValues,
			valuesFiles,
			5*time.Minute)
		return err
	})

	// Validate the game server status.
	err = targetEnv.WaitForServerToBeReady(cmd.Context(), taskRunner)
	if err != nil {
		return err
	}

	// Run the tasks.
	if err = taskRunner.Run(); err != nil {
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ Game server successfully deployed"))
	return nil
}

// fetchLocalDockerImageMetadata retrieves metadata from a local Docker image.
func fetchLocalDockerImageMetadata(imageRef string) (*v1.ConfigFile, error) {
	// Parse the image reference (name + tag or digest)
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse docker image reference: %w", err)
	}

	// Load the image from the local Docker daemon
	img, err := daemon.Image(ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get local docker image: %w", err)
	}

	// Fetch the image configuration blob
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get docker image config file: %w", err)
	}

	return cfg, nil
}

// fetchRemoteDockerImageMetadata retrieves the labels of an image in a remote Docker registry.
func fetchRemoteDockerImageMetadata(creds *envapi.DockerCredentials, imageRef string) (*v1.ConfigFile, error) {
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
		return nil, fmt.Errorf("failed to get remote docker image descriptor: %w", err)
	}

	// Fetch the image configuration blob
	img, err := desc.Image()
	if err != nil {
		return nil, fmt.Errorf("failed to get docker image from descriptor: %w", err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get docker image config file: %w", err)
	}

	// Return the labels from the configuration
	return cfg, nil
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
