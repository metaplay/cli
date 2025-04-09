/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/dustin/go-humanize"
	v1 "github.com/google/go-containerregistry/pkg/v1"
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
	UsePositionalArgs

	argEnvironment          string
	argImageNameTag         string
	extraArgs               []string
	flagHelmReleaseName     string
	flagHelmChartLocalPath  string
	flagHelmChartRepository string
	flagHelmChartVersion    string
	flagHelmValuesPath      string
}

func init() {
	o := deployGameServerOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgumentOpt(&o.argImageNameTag, "[IMAGE:]TAG", "Docker image name and tag, eg, 'mygame:364cff09' or '364cff09'.")
	args.SetExtraArgs(&o.extraArgs, "Passed as-is to Helm.")

	cmd := &cobra.Command{
		Use:     "server ENVIRONMENT [IMAGE:]TAG [flags] [-- EXTRA_ARGS]",
		Aliases: []string{"srv"},
		Short:   "Deploy a server image into the target environment",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
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

			{Arguments}

			Related commands:
			- 'metaplay build image ...' to build the docker image.
			- 'metaplay image push ...' to push the built image to the environment.
			- 'metaplay debug logs ...' to view logs from the deployed server.
			- 'metaplay debug shell ...' to start a shell on a running server pod.
		`),
		Example: renderExample(`
			# Push the local image and deploy to the environment tough-falcons.
			metaplay deploy server tough-falcons mygame:364cff09

			# Deploy an image that has already been pushed into the environment.
			metaplay deploy server tough-falcons 364cff09

			# Deploy the latest locally built image for this project.
			metaplay deploy server tough-falcons latest-local

			# Pass extra arguments to Helm.
			metaplay deploy server tough-falcons mygame:364cff09 -- --set-string config.image.pullPolicy=Always

			# Use Helm chart from the local disk.
			metaplay deploy server tough-falcons mygame:364cff09 --local-chart-path=/path/to/metaplay-gameserver

			# Override the Helm chart repository and version.
			metaplay deploy server tough-falcons mygame:364cff09 --helm-chart-repo=https://custom-repo.domain.com --helm-chart-version=0.7.0

			# Override the Helm release name.
			metaplay deploy server tough-falcons mygame:364cff09 --helm-release-name=my-release-name
		`),
	}
	deployCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagHelmReleaseName, "helm-release-name", "", "Helm release name to use for the game server deployment (default to '<environmentID>-gameserver')")
	flags.StringVar(&o.flagHelmChartLocalPath, "local-chart-path", "", "Path to a local version of the metaplay-gameserver chart (repository and version are ignored if this is set)")
	flags.StringVar(&o.flagHelmChartRepository, "helm-chart-repo", "", "Override for Helm chart repository to use for the metaplay-gameserver chart")
	flags.StringVar(&o.flagHelmChartVersion, "helm-chart-version", "", "Override for Helm chart version to use, eg, '0.7.0'")
	flags.StringVarP(&o.flagHelmValuesPath, "values", "f", "", "Override for path to the Helm values file, e.g., 'Backend/Deployments/develop-server.yaml'")
}

func (o *deployGameServerOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *deployGameServerOpts) Run(cmd *cobra.Command) error {
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
		helmChartVersion := project.Config.ServerChartVersion
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

	// Get docker credentials.
	dockerCredentials, err := targetEnv.GetDockerCredentials(envDetails)
	if err != nil {
		return fmt.Errorf("failed to get docker credentials: %v", err)
	}
	log.Debug().Msgf("Got docker credentials: username=%s", dockerCredentials.Username)

	// If no docker image specified, scan the images matching project from the local docker repo
	// and then let the user choose from the images.
	if o.argImageNameTag == "" {
		selectedImage, err := selectDockerImageInteractively("Select Image to Deploy", project.Config.ProjectHumanID)
		if err != nil {
			return err
		}
		o.argImageNameTag = selectedImage.RepoTag
	} else if o.argImageNameTag == "latest-local" {
		// Resolve the local docker images matching project human ID.
		localImages, err := envapi.ReadLocalDockerImagesByProjectID(project.Config.ProjectHumanID)
		if err != nil {
			return err
		}

		// If there are no images for this project, error out.
		if len(localImages) == 0 {
			return fmt.Errorf("no docker images matching project '%s' found locally; build an image first with 'metaplay build image'", project.Config.ProjectHumanID)
		}

		// Use the first entry (they are reverse sorted by creation time).
		o.argImageNameTag = localImages[0].RepoTag
	}

	// Push the image to the remote repository (if full name is specified).
	useLocalImage := strings.Contains(o.argImageNameTag, ":")
	var imageTag string
	var imageConfig *v1.ConfigFile
	if useLocalImage {
		// Resolve metadata from local image.
		imageConfig, err = envapi.ReadLocalDockerImageMetadata(o.argImageNameTag)
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
		imageConfig, err = envapi.FetchRemoteDockerImageMetadata(dockerCredentials, remoteImageName)
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
		helmChartRepo := coalesceString(project.Config.HelmChartRepository, o.flagHelmChartRepository, "https://charts.metaplay.dev")
		minChartVersion, _ := version.NewVersion("0.7.0")
		useHelmChartVersion, err = helmutil.ResolveBestMatchingHelmVersion(helmChartRepo, metaplayGameServerChartName, minChartVersion, chartVersionConstraints)
		helmChartPath = helmutil.GetHelmChartPath(helmChartRepo, metaplayGameServerChartName, useHelmChartVersion)
		if err != nil {
			return err
		}
	}
	log.Debug().Msgf("Helm chart path: %s", helmChartPath)

	// Resolve Helm values file path relative to current directory.
	valuesFiles := project.GetServerValuesFiles(envConfig)

	// Create a Kubernetes client.
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(kubeCli.KubeConfig, envConfig.GetKubernetesNamespace())
	if err != nil {
		return fmt.Errorf("failed to initialize Helm config: %v", err)
	}

	// Determine if there's an existing release deployed.
	existingRelease, err := helmutil.GetExistingRelease(actionConfig, metaplayGameServerChartName)
	if err != nil {
		return err
	}

	// If migrating from chart version <0.8.0 to >=0.8.0, uninstall the old release first to avoid the
	// old and new operators from modifying the same resources.
	uninstallExisting := false
	if existingRelease != nil && existingRelease.Chart != nil && existingRelease.Chart.Metadata != nil {
		log.Debug().Msgf("Existing Helm release '%s' found with chart version %s", existingRelease.Name, existingRelease.Chart.Metadata.Version)

		// Parse the new chart version.
		newVersion, err := semver.NewVersion(useHelmChartVersion)
		if err != nil {
			return fmt.Errorf("failed to parse Helm chart version '%s': %v", useHelmChartVersion, err)
		}

		// Parse existing chart version.
		existingVersion, err := semver.NewVersion(existingRelease.Chart.Metadata.Version)
		if err != nil {
			log.Warn().Err(err).Msgf("Failed to parse existing Helm chart version '%s'. Assuming it might be the old operator, proceeding with deploy carefully.", existingRelease.Chart.Metadata.Version)
			uninstallExisting = true
		}

		// Check if crossing the v0.8.0 threshold (in either direction).
		threshold := semver.MustParse("0.8.0")
		newAboveV080 := newVersion.GreaterThanEqual(threshold)
		existingAboveV080 := existingVersion.GreaterThanEqual(threshold)
		if newAboveV080 != existingAboveV080 {
			log.Info().Msgf("Going from Helm chart v%s to v%s. Must uninstall existing release before installing new one.", existingRelease.Chart.Metadata.Version, useHelmChartVersion)
			uninstallExisting = true
		}
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
		"environment":       envConfig.Name,
		"environmentFamily": envConfig.GetEnvironmentFamily(),
		"config": map[string]interface{}{
			"files": []string{
				"./Config/Options.base.yaml",
				envConfig.GetEnvironmentSpecificRuntimeOptionsFile(),
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

	// Resolve Helm release name. If not specified, default to:
	// - Earlier name if a deployment already exists.
	// - '<environmentID>-gameserver' otherwise.
	helmReleaseName := o.flagHelmReleaseName
	helmReleaseNameBadge := ""
	if helmReleaseName == "" {
		if existingRelease != nil {
			helmReleaseName = existingRelease.Name
			if uninstallExisting {
				helmReleaseNameBadge = styles.RenderMuted("[uninstall existing]")
			} else {
				helmReleaseNameBadge = styles.RenderMuted("[update existing]")
			}
		} else {
			helmReleaseName = fmt.Sprintf("%s-gameserver", envConfig.HumanID)
			helmReleaseNameBadge = styles.RenderMuted("[default]")
		}
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Deploy Game Server to Cloud"))
	log.Info().Msg("")

	// Show info.
	log.Info().Msgf("Target environment:")
	log.Info().Msgf("  Name:               %s", styles.RenderTechnical(envConfig.Name))
	log.Info().Msgf("  ID:                 %s", styles.RenderTechnical(envConfig.HumanID))
	log.Info().Msgf("  Type:               %s", styles.RenderTechnical(string(envConfig.Type)))
	log.Info().Msgf("  Stack domain:       %s", styles.RenderTechnical(envConfig.StackDomain))
	log.Info().Msgf("Build information:")
	if useLocalImage {
		log.Info().Msgf("  Image name:         %s", styles.RenderTechnical(o.argImageNameTag))
	} else {
		log.Info().Msgf("  Image name:         %s", styles.RenderTechnical(fmt.Sprintf("%s:%s", envDetails.Deployment.EcrRepo, imageTag)))
	}
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
	log.Info().Msgf("  Helm release name:  %s %s", styles.RenderTechnical(helmReleaseName), helmReleaseNameBadge)
	if len(valuesFiles) > 0 {
		log.Info().Msgf("  Helm values files:  %s", styles.RenderTechnical(strings.Join(valuesFiles, ", ")))
	}
	// \todo list of runtime options files
	log.Info().Msg("")

	taskRunner := tui.NewTaskRunner()

	// If using local image, add task to push it.
	if useLocalImage {
		taskRunner.AddTask("Push docker image to environment repository", func(output *tui.TaskOutput) error {
			return pushDockerImage(cmd.Context(), output, o.argImageNameTag, envDetails.Deployment.EcrRepo, dockerCredentials)
		})
	}

	// If migrating from old operator to new operator, uninstall the old release first.
	if uninstallExisting {
		taskRunner.AddTask("Uninstall existing game server", func(output *tui.TaskOutput) error {
			err := helmutil.UninstallRelease(actionConfig, existingRelease)
			existingRelease = nil // Mark as uninstalled, so deploy doesn't try to upgrade
			return err
		})
	}

	// Install or upgrade the Helm chart.
	taskRunner.AddTask("Deploy game server using Helm", func(output *tui.TaskOutput) error {
		_, err := helmutil.HelmUpgradeOrInstall(
			output,
			actionConfig,
			existingRelease,
			envConfig.GetKubernetesNamespace(),
			helmReleaseName,
			helmChartPath,
			useHelmChartVersion,
			valuesFiles,
			helmValues,
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

	log.Info().Msg(styles.RenderSuccess("✅ Game server successfully deployed!"))
	return nil
}

func selectDockerImageInteractively(title string, projectHumanID string) (*envapi.MetaplayImageInfo, error) {
	// Resolve the local docker images matching project human ID.
	localImages, err := envapi.ReadLocalDockerImagesByProjectID(projectHumanID)
	if err != nil {
		return nil, err
	}

	// If there are no images for this project, error out.
	if len(localImages) == 0 {
		return nil, fmt.Errorf("no docker images matching project '%s' found locally; build an image first with 'metaplay build image'", projectHumanID)
	}

	// Let the user choose from the list of images.
	selectedImage, err := tui.ChooseFromListDialog(
		title,
		localImages,
		func(img *envapi.MetaplayImageInfo) (string, string) {
			description := humanize.Time(img.ConfigFile.Created.Time)
			return img.RepoTag, description
		})
	if err != nil {
		return nil, err
	}

	log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), selectedImage.RepoTag)
	return selectedImage, nil
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
