/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	v1 "helm.sh/helm/v4/pkg/release/v1"
)

type getServerInfoOpts struct {
	UsePositionalArgs

	argEnvironment string
	flagFormat     string
}

// serverInfo represents the complete server deployment information
type serverInfo struct {
	PortalInfo  *portalapi.EnvironmentInfo `json:"portal_info"`
	HelmRelease *helmReleaseInfo           `json:"helm_release"`
	ImageInfo   *deploymentImageInfo       `json:"image_info"`
}

type helmReleaseInfo struct {
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	ChartName    string    `json:"chart_name"`
	ChartVersion string    `json:"chart_version"`
	Namespace    string    `json:"namespace"`
	LastDeployed time.Time `json:"last_deployed"`
	Revision     int       `json:"revision"`
}

type deploymentImageInfo struct {
	ImageTag     string    `json:"tag"`
	BuildNumber  string    `json:"build_number"`
	CommitID     string    `json:"commit_id"`
	SdkVersion   string    `json:"sdk_version"`
	CreationTime time.Time `json:"creation_time,omitempty"`
}

func init() {
	o := getServerInfoOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")

	cmd := &cobra.Command{
		Use:     "server-info ENVIRONMENT [flags]",
		Aliases: []string{"srv-info"},
		Short:   "Get information about the game server deployment",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is currently in preview and may change in future releases.

			This command shows details about the game server deployment running in the cloud,
			including information about the Helm release and the deployed container image.

			By default, displays the most relevant information in a human-readable text format.
			Use --format=json to get the complete server information in JSON format.
			WARNING: The JSON output is subject to change!

			{Arguments}

			Related commands:
			- 'metaplay get env-info ...' to get information about the target environment.
			- 'metaplay debug server-status ...' to get diagnostics about the health of the deployment.
			- 'metaplay deploy server ...' to deploy a game server.
		`),
		Example: renderExample(`
			# Show server deployment information in text format (default)
			metaplay get server-info nimbly
		`),
	}

	getCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagFormat, "format", "text", "Output format. Valid values are 'text' or 'json'")
}

func (o *getServerInfoOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate format
	if o.flagFormat != "text" && o.flagFormat != "json" {
		return fmt.Errorf("invalid format %q, must be either 'text' or 'json'", o.flagFormat)
	}

	return nil
}

func (o *getServerInfoOpts) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve environment.
	envConfig, tokenSet, err := resolveEnvironment(ctx, project, o.argEnvironment)
	if err != nil {
		return err
	}

	// Create TargetEnvironment.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Gather server deployment information.
	info, err := o.gatherDeployedServerInfo(ctx, targetEnv, envConfig)
	if err != nil {
		return err
	}

	// Output based on format.
	if o.flagFormat == "json" {
		// Pretty-print as JSON for full details
		serverInfoJSON, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return err
		}
		log.Info().Msg(string(serverInfoJSON))
	} else {
		// Environment header
		log.Info().Msg("")
		log.Info().Msg(styles.RenderTitle("Deployed Server Info"))
		log.Info().Msg("")

		// Portal Information (if available)
		if info.PortalInfo != nil {
			log.Info().Msg("Portal information:")
			log.Info().Msgf("  %-19s %s", "Name:", styles.RenderTechnical(info.PortalInfo.Name))
			log.Info().Msgf("  %-19s %s", "Human ID:", styles.RenderTechnical(info.PortalInfo.HumanID))
			log.Info().Msgf("  %-19s %s", "Environment family:", styles.RenderTechnical(string(info.PortalInfo.Type)))
			log.Info().Msgf("  %-19s %s", "Hosting type:", styles.RenderTechnical(string(info.PortalInfo.HostingType)))
			log.Info().Msgf("  %-19s %s", "Stack domain:", styles.RenderTechnical(info.PortalInfo.StackDomain))
		}

		// Helm Release Information
		log.Info().Msg("")
		if info.HelmRelease != nil {
			log.Info().Msg("Helm release:")
			log.Info().Msgf("  %-19s %s", "Chart name:", styles.RenderTechnical(info.HelmRelease.ChartName))
			log.Info().Msgf("  %-19s %s", "Chart version:", styles.RenderTechnical(info.HelmRelease.ChartVersion))
			log.Info().Msgf("  %-19s %s", "Release name:", styles.RenderTechnical(info.HelmRelease.Name))
			log.Info().Msgf("  %-19s %s", "Status:", styles.RenderTechnical(info.HelmRelease.Status))
			log.Info().Msgf("  %-19s %s", "Namespace:", styles.RenderTechnical(info.HelmRelease.Namespace))
			log.Info().Msgf("  %-19s %s", "Revision:", styles.RenderTechnical(fmt.Sprintf("%d", info.HelmRelease.Revision)))
			if !info.HelmRelease.LastDeployed.IsZero() {
				log.Info().Msgf("  %-19s %s", "Last Deployed:", styles.RenderTechnical(humanize.Time(info.HelmRelease.LastDeployed)))
			}
		} else {
			log.Info().Msg("No game server deployment found")
		}

		// Image Information
		if info.ImageInfo != nil {
			log.Info().Msg("")
			log.Info().Msg("Image information:")
			log.Info().Msgf("  %-19s %s", "Image tag:", styles.RenderTechnical(info.ImageInfo.ImageTag))
			log.Info().Msgf("  %-19s %s", "Commit ID:", styles.RenderTechnical(info.ImageInfo.CommitID))
			log.Info().Msgf("  %-19s %s", "Build number:", styles.RenderTechnical(info.ImageInfo.BuildNumber))
			log.Info().Msgf("  %-19s %s", "SDK version:", styles.RenderTechnical(info.ImageInfo.SdkVersion))
			log.Info().Msgf("  %-19s %s", "Created:", styles.RenderTechnical(humanize.Time(info.ImageInfo.CreationTime)))
		}
	}

	return nil
}

func (o *getServerInfoOpts) gatherDeployedServerInfo(ctx context.Context, targetEnv *envapi.TargetEnvironment, envConfig *metaproj.ProjectEnvironmentConfig) (*serverInfo, error) {
	var err error

	// Fetch portal information if targeting a managed stack
	authProviderName := coalesceString(envConfig.AuthProvider, "metaplay")
	var portalInfo *portalapi.EnvironmentInfo
	if authProviderName == "metaplay" {
		portalClient := portalapi.NewClient(targetEnv.TokenSet)
		portalInfo, err = portalClient.FetchEnvironmentInfoByHumanID(envConfig.HumanID)
		if err != nil {
			log.Debug().Err(err).Msg("Failed to fetch portal environment info")
		}
	}

	// Get Kubernetes client
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes client: %w", err)
	}

	// Get Helm action config
	actionConfig, err := helmutil.NewActionConfig(kubeCli.KubeConfig, envConfig.GetKubernetesNamespace())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Helm config: %w", err)
	}

	// Get the existing release for image info extraction
	existingRelease, err := helmutil.GetExistingRelease(actionConfig, metaplayGameServerChartName)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing Helm release: %w", err)
	}

	// Extract detailed release information, if a release exists.
	var helmInfo *helmReleaseInfo
	var imageInfo *deploymentImageInfo
	if existingRelease != nil {
		helmInfo, err = o.getHelmReleaseInfo(existingRelease)
		if err != nil {
			return nil, fmt.Errorf("failed to get Helm release info: %w", err)
		}

		imageInfo, err = o.getImageInfo(ctx, targetEnv, existingRelease)
		if err != nil {
			return nil, fmt.Errorf("failed to get image info: %w", err)
		}
	}

	// Return the combined info
	return &serverInfo{
		PortalInfo:  portalInfo,
		HelmRelease: helmInfo,
		ImageInfo:   imageInfo,
	}, nil
}

// Extract the Helm release information from a release object into a simpler info class.
func (o *getServerInfoOpts) getHelmReleaseInfo(releaseInfo *v1.Release) (*helmReleaseInfo, error) {
	helmInfo := &helmReleaseInfo{
		Name:         releaseInfo.Name,
		Status:       releaseInfo.Info.Status.String(),
		Namespace:    releaseInfo.Namespace,
		Revision:     releaseInfo.Version,
		LastDeployed: releaseInfo.Info.LastDeployed,
	}

	if releaseInfo.Chart != nil && releaseInfo.Chart.Metadata != nil {
		helmInfo.ChartName = releaseInfo.Chart.Metadata.Name
		helmInfo.ChartVersion = releaseInfo.Chart.Metadata.Version
	}

	return helmInfo, nil
}

// Extract information about the docker image used in the game server deployment.
func (o *getServerInfoOpts) getImageInfo(ctx context.Context, targetEnv *envapi.TargetEnvironment, existingRelease *v1.Release) (*deploymentImageInfo, error) {
	// Extract image information from Helm release values.
	var imageTag, fullImageRef string
	if existingRelease.Config != nil {
		if imageConfig, ok := existingRelease.Config["image"].(map[string]any); ok {
			if tag, ok := imageConfig["tag"].(string); ok {
				imageTag = tag
			}
			if repository, ok := imageConfig["repository"].(string); ok && imageTag != "" {
				fullImageRef = fmt.Sprintf("%s:%s", repository, imageTag)
			}
		}

		// Bail out if unable to determine image reference -- this happens with legacy SDK versions.
		// \todo Figure out a more robust implementation; grab from the GameServer resource directly?
		if fullImageRef == "" {
			return nil, fmt.Errorf("no image information found in Helm release; operation is not supported for Helm chart versions earlier than v0.8.0")
		}
	} else {
		return nil, fmt.Errorf("no image information found in Helm release")
	}

	// Get environment details.
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return nil, err
	}

	// Get docker credentials for the image registry.
	dockerCredentials, err := targetEnv.GetDockerCredentials(envDetails)
	if err != nil {
		return nil, err
	}

	// Fetch image metadata from the remote docker repository.
	imageMetadata, err := envapi.FetchRemoteDockerImageMetadata(dockerCredentials, fullImageRef)
	if err != nil {
		return nil, err
	}

	return &deploymentImageInfo{
		ImageTag:     imageMetadata.Tag,
		BuildNumber:  imageMetadata.BuildNumber,
		CommitID:     imageMetadata.CommitID,
		SdkVersion:   imageMetadata.SdkVersion,
		CreationTime: imageMetadata.CreatedTime,
	}, nil
}
