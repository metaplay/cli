/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"fmt"
	"strings"
	"time"

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
	flagCheckOnly           bool // Only perform the server status check, not the deployment itself. \todo separate to its own command?
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
		Example: trimIndent(`
			# Push the local image and deploy to the environment tough-falcons.
			metaplay deploy server tough-falcons mygame:364cff09

			# Deploy an image that has already been pushed into the environment.
			metaplay deploy server tough-falcons 364cff09

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
	flags.BoolVar(&o.flagCheckOnly, "check-only", false, "Use this flag to only perform the server status check (docker image push and deploy are skipped)")
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
	authProvider := getAuthProvider(project)

	// Ensure the user is logged in
	tokenSet, err := tui.RequireLoggedIn(cmd.Context(), authProvider)
	if err != nil {
		return err
	}

	// Resolve project and environment.
	envConfig, err := resolveEnvironment(project, tokenSet, o.argEnvironment)
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

	// If no docker image specified, scan the images matching project from the local docker repo
	// and then let the user choose from the images.
	if o.argImageNameTag == "" {
		// Resolve the local docker images matching project human ID.
		localImages, err := envapi.ReadLocalDockerImagesByProjectID(project.Config.ProjectHumanID)
		if err != nil {
			return err
		}

		// Let the user choose from the list of images.
		selectedImage, err := tui.ChooseFromListDialog(
			"Select Docker Image to Deploy",
			localImages,
			func(img *envapi.MetaplayImageInfo) (string, string) {
				description := fmt.Sprintf("[%s, %s, %s]", humanize.Time(img.ConfigFile.Created.Time), img.CommitID, img.BuildNumber)
				return img.RepoTag, description
			})
		if err != nil {
			return err
		}

		log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), selectedImage.RepoTag)
		o.argImageNameTag = selectedImage.RepoTag
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
		"environmentFamily": envConfig.GetEnvironmentFamily(),
		"config": map[string]interface{}{
			"files": []string{
				"./Config/Options.base.yaml",
				envConfig.GetEnvironmentSpecificRuntimeOptionsFile(),
			},
		},
		// DEBUG DEBUG Opt into the new operator
		// "experimental": map[string]interface{}{
		// 	"gameserversV0Api": map[string]interface{}{
		// 		"enabled": true,
		// 	},
		// },
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
			helmReleaseNameBadge = styles.RenderMuted("[existing]")
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
	log.Info().Msgf("  Helm release name:  %s %s", styles.RenderTechnical(helmReleaseName), helmReleaseNameBadge)
	if len(valuesFiles) > 0 {
		log.Info().Msgf("  Helm values files:  %s", styles.RenderTechnical(strings.Join(valuesFiles, ", ")))
	}
	// \todo list of runtime options files
	log.Info().Msg("")

	taskRunner := tui.NewTaskRunner()

	// If using local image, add task to push it.
	if useLocalImage {
		taskRunner.AddTask("Push docker image to environment repository", func() error {
			// Skip push if --check-only is used.
			if o.flagCheckOnly {
				return nil
			}

			return pushDockerImage(cmd.Context(), o.argImageNameTag, envDetails.Deployment.EcrRepo, dockerCredentials)
		})
	}

	// Install or upgrade the Helm chart.
	taskRunner.AddTask("Deploy game server using Helm", func() error {
		// Skip deploy if --check-only is used.
		if o.flagCheckOnly {
			return nil
		}

		_, err := helmutil.HelmUpgradeOrInstall(
			actionConfig,
			existingRelease,
			envConfig.GetKubernetesNamespace(),
			helmReleaseName,
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

	log.Info().Msg(styles.RenderSuccess("✅ Game server successfully deployed!"))
	return nil
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
