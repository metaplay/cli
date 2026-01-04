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
	"github.com/hashicorp/go-version"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/release"
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
	flagDryRun              bool
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

			After deploying the server image, various checks are run against the deployment to
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
	flags.BoolVar(&o.flagDryRun, "dry-run", false, "Show what would be deployed without actually performing the deployment")
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

	// Check that docker is installed and running
	log.Debug().Msgf("Check if docker is available")
	err = checkDockerAvailable()
	if err != nil {
		return err
	}

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

	// Resolve image tag and metadata from the local or remote image.
	useLocalImage := strings.Contains(o.argImageNameTag, ":")
	var imageTag string
	var imageInfo *envapi.MetaplayImageInfo
	if useLocalImage {
		// Resolve metadata from local image.
		imageInfo, err = envapi.ReadLocalDockerImageMetadata(o.argImageNameTag)
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

		// Fetch the image info from the remote docker image.
		remoteImageName := fmt.Sprintf("%s:%s", envDetails.Deployment.EcrRepo, imageTag)
		imageInfo, err = envapi.FetchRemoteDockerImageMetadata(dockerCredentials, remoteImageName)
		if err != nil {
			return err
		}
	}

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

	// For Metaplay-managed environments, check that the local env config (from metaplay-project.yaml)
	// matches the one from portal.
	if envConfig.HostingType == portalapi.HostingTypeMetaplayHosted {
		portalClient := portalapi.NewClient(targetEnv.TokenSet)

		// Fetch info from the portal.
		portalInfo, err := portalClient.FetchEnvironmentInfoByHumanID(envConfig.HumanID)
		if err != nil {
			return err
		}

		// Environment type (prod, staging, development) must match that in the portal.
		// Otherwise, the game server will be using wrong environment type-specific defaults.
		if envConfig.Type != portalInfo.Type {
			return clierrors.Newf("Environment type mismatch: local config has '%s', portal has '%s'", envConfig.Type, portalInfo.Type).
				WithSuggestion("Run 'metaplay update project-environments' to sync with portal")
		}
	}

	// Default shard config based on environment type.
	// \todo Auto-detect these from the infrastructure.
	var shardsConfig []map[string]any
	if envConfig.Type == portalapi.EnvironmentTypeProduction || envConfig.Type == portalapi.EnvironmentTypeStaging {
		shardsConfig = []map[string]any{
			{
				"name":      "all",
				"singleton": true,
				"requests": map[string]any{
					"cpu":    "1500m",
					"memory": "3000M",
				},
			},
		}
	} else {
		shardsConfig = []map[string]any{
			{
				"name":      "all",
				"singleton": true,
				"requests": map[string]any{
					"cpu":    "250m",
					"memory": "500Mi",
				},
			},
		}
	}

	// Convert shardConfig to []any to avoid JSON schema validation type errors.
	// This happens because Helm, or https://github.com/santhosh-tekuri/jsonschema where the inputs are validated,
	// doesn't allow []map[string]any. Its typeOf() function only accepts `[]any` as array types, not other types
	// of arrays, like []map[string]any.
	// Bug report in Helm: https://github.com/helm/helm/issues/31148 -- if the issue gets fixed, this code can be removed.
	untypedShardsConfig := make([]any, len(shardsConfig))
	for i, v := range shardsConfig {
		untypedShardsConfig[i] = v
	}

	// Default Helm values. The user Helm values files are applied on top so
	// all these values can be overridden by the user.
	// \todo check for the existence of the runtime options files
	helmDefaultValues := map[string]any{
		"environment":       envConfig.Name,
		"environmentFamily": envConfig.GetEnvironmentFamily(),
		"config": map[string]any{
			"files": []any{
				"./Config/Options.base.yaml",
				envConfig.GetEnvironmentSpecificRuntimeOptionsFile(),
			},
		},
		"tenant": map[string]any{
			"discoveryEnabled": true,
		},
		"sdk": map[string]any{
			"version": imageInfo.SdkVersion,
		},
		"shards": untypedShardsConfig,
	}
	helmRequiredValues := map[string]any{
		"image": map[string]any{
			"tag":        imageTag,
			"repository": envDetails.Deployment.EcrRepo,
		},
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
	log.Info().Msg("")
	log.Info().Msgf("Build information:")
	if useLocalImage {
		log.Info().Msgf("  Image name:         %s", styles.RenderTechnical(o.argImageNameTag))
	} else {
		log.Info().Msgf("  Image name:         %s", styles.RenderTechnical(fmt.Sprintf("%s:%s", envDetails.Deployment.EcrRepo, imageTag)))
	}
	log.Info().Msgf("  Build number:       %s", styles.RenderTechnical(imageInfo.BuildNumber))
	log.Info().Msgf("  Commit ID:          %s", styles.RenderTechnical(imageInfo.CommitID))
	log.Info().Msgf("  Created:            %s", styles.RenderTechnical(humanize.Time(imageInfo.CreatedTime)))
	log.Info().Msgf("  Metaplay SDK:       %s", styles.RenderTechnical(imageInfo.SdkVersion))
	log.Info().Msg("")
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
	// Show current Helm release info if it exists.
	if existingRelease != nil {
		log.Info().Msg("")
		log.Info().Msg("Existing deployment:")
		if existingRelease.Chart != nil && existingRelease.Chart.Metadata != nil {
			log.Info().Msgf("  %-19s %s", "Chart version:", styles.RenderTechnical(existingRelease.Chart.Metadata.Version))
		}
		log.Info().Msgf("  %-19s %s", "Status:", styles.RenderTechnical(existingRelease.Info.Status.String()))
		log.Info().Msgf("  %-19s %s", "Revision:", styles.RenderTechnical(fmt.Sprintf("%d", existingRelease.Version)))
		lastDeployedAt := "Unknown"
		if !existingRelease.Info.LastDeployed.IsZero() {
			lastDeployedAt = humanize.Time(existingRelease.Info.LastDeployed.Time)
		}
		log.Info().Msgf("  %-19s %s", "Last Deployed:", styles.RenderTechnical(lastDeployedAt))
	}
	log.Info().Msg("")

	// Check if the existing release is in some kind of pending state
	uninstallExistingRelease := false
	if existingRelease != nil {
		releaseStatus := existingRelease.Info.Status
		if releaseStatus == release.StatusUninstalling {
			log.Error().Msgf("Helm release is in state 'uninstalling'; try again later or manually uninstall the server with %s", styles.RenderPrompt("metaplay remove server"))
			return fmt.Errorf("unable to deploy server: existing Helm release is in state 'uninstalling'")
		} else if releaseStatus.IsPending() {
			log.Warn().Msgf("Helm release is in pending state '%s', previous release will be removed before deploying the new version", releaseStatus)
			uninstallExistingRelease = true
		}
		log.Debug().Msgf("Existing Helm release info: %+v", existingRelease.Info)
	}

	// If dry-run mode, stop here.
	if o.flagDryRun {
		log.Info().Msg(styles.RenderMuted("Dry-run mode: skipping deployment"))
		return nil
	}

	// Use TaskRunner to visualize progress.
	taskRunner := tui.NewTaskRunner()

	// If using local image, add task to push it.
	if useLocalImage {
		taskRunner.AddTask("Push docker image to environment repository", func(output *tui.TaskOutput) error {
			return pushDockerImage(cmd.Context(), output, o.argImageNameTag, envDetails.Deployment.EcrRepo, dockerCredentials)
		})
	}

	// If there's a pending release, uninstall it first.
	if uninstallExistingRelease {
		taskRunner.AddTask(fmt.Sprintf("Uninstall existing Helm release"), func(output *tui.TaskOutput) error {
			output.SetHeaderLines([]string{
				fmt.Sprintf("Release status: %s", existingRelease.Info.Status),
			})
			err := helmutil.UninstallRelease(actionConfig, existingRelease)
			if err != nil {
				return fmt.Errorf("failed to uninstall Helm release %s: %v", existingRelease.Name, err)
			}
			existingRelease = nil // Mark as uninstalled, so deploy doesn't try to upgrade
			return nil
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

	// Figure out whether the values file JSON schema can be validated:
	// - v0.9+ (including v1.x+, v0.10.x+, and prereleases) can be validated.
	// - v0.8.1+ (including prereleases) can be validated, but v0.8.0 cannot.
	// - v0.7.x and earlier cannot be validated.
	// - Local charts are validated (we assume recent versions are used).
	validateJsonSchema := false
	if useHelmChartVersion != "local" {
		chartVersion, err := semver.NewVersion(useHelmChartVersion)
		if err != nil {
			log.Warn().Err(err).Msgf("Failed to parse Helm chart version '%s', skipping schema validation", useHelmChartVersion)
			validateJsonSchema = false
		} else {
			major := chartVersion.Major()
			minor := chartVersion.Minor()
			patch := chartVersion.Patch()

			if major >= 1 || (major == 0 && minor >= 9) {
				// v0.9 and later can be validated (including v0.10.x, v1.x.x and later, and v0.9.x-pre versions)
				validateJsonSchema = true
			} else if major == 0 && minor == 8 {
				// For v0.8 series: don't validate for v0.8.0, but do validate for >=v0.8.1 (including pre releases)
				if patch == 0 {
					// Exactly v0.8.0 cannot be validated
					validateJsonSchema = false
				} else {
					// v0.8.1+ (including prereleases) can be validated
					validateJsonSchema = true
				}
			} else {
				// v0.7 and earlier cannot be validated
				log.Warn().Msgf("Helm chart version '%s' is below minimum supported version, skipping schema validation", useHelmChartVersion)
				validateJsonSchema = false
			}

			log.Debug().Msgf("Helm chart version '%s': schema validation %s", useHelmChartVersion,
				map[bool]string{true: "enabled", false: "disabled"}[validateJsonSchema])
		}
	} else {
		// For local charts, we assume recent versions, and enable validation.
		// \todo Add flag for disabling this, if needed.
		log.Debug().Msg("Using local Helm chart, enable schema validation")
		validateJsonSchema = true
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
			helmDefaultValues,
			helmRequiredValues,
			5*time.Minute,
			validateJsonSchema)
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
			description := humanize.Time(img.CreatedTime)
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
