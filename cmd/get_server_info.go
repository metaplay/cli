/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/action"
	corev1 "k8s.io/api/core/v1"
)

// \todo Improvements to the command:
// - Show deployment history

type getServerInfoOpts struct {
	UsePositionalArgs

	argEnvironment string
	flagFormat     string
}

// serverInfo represents the complete server deployment information
type serverInfo struct {
	HelmRelease *helmReleaseInfo `json:"helm_release,omitempty"`
	PodStatus   *podStatusInfo   `json:"pod_status,omitempty"`
	ImageInfo   *imageInfo       `json:"image_info,omitempty"`
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

type gameServerPodStatus struct {
	Ready         bool   `json:"ready"`
	StatusMessage string `json:"status_message"`
}

type podDetail struct {
	Name          string        `json:"name"`
	Phase         string        `json:"phase"`
	Ready         bool          `json:"ready"`
	Restarts      int32         `json:"restarts"`
	Age           time.Duration `json:"age"`
	StatusMessage string        `json:"status_message"`
}

type shardInfo struct {
	ShardSetName  string      `json:"shard_set_name"`
	Replicas      int32       `json:"replicas"`
	ReadyReplicas int32       `json:"ready_replicas"`
	Pods          []podDetail `json:"pods"`
}

type podStatusInfo struct {
	TotalPods   int         `json:"total_pods"`
	ReadyPods   int         `json:"ready_pods"`
	RunningPods int         `json:"running_pods"`
	PendingPods int         `json:"pending_pods"`
	FailedPods  int         `json:"failed_pods"`
	Shards      []shardInfo `json:"shards"`
}

type imageInfo struct {
	ImageTag     string    `json:"tag"`
	BuildNumber  string    `json:"build_number"`
	CommitID     string    `json:"commit_id"`
	SdkVersion   string    `json:"sdk_version"`
	CreationTime time.Time `json:"creation_time,omitempty"`
}

func init() {
	o := getServerInfoOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")

	cmd := &cobra.Command{
		Use:     "server-info ENVIRONMENT [flags]",
		Aliases: []string{"srv-info"},
		Short:   "Get information about the game server deployment",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Get comprehensive information about the game server deployment in the target environment.

			This command shows details about the Helm release, image information, and pod status for the
			deployed game server.

			By default, displays the most relevant information in a human-readable text format.
			Use --format=json to get the complete server information in JSON format.

			{Arguments}
		`),
		Example: renderExample(`
			# Show server deployment information in text format (default)
			metaplay get server-info tough-falcons

			# Show complete server information in JSON format
			metaplay get server-info tough-falcons --format=json
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

		// Helm Release Information
		if info.HelmRelease != nil {
			log.Info().Msg("Helm release:")
			log.Info().Msgf("  %-15s %s", "Chart:", styles.RenderTechnical(fmt.Sprintf("%s:%s", info.HelmRelease.ChartName, info.HelmRelease.ChartVersion)))
			log.Info().Msgf("  %-15s %s", "Release name:", styles.RenderTechnical(info.HelmRelease.Name))
			log.Info().Msgf("  %-15s %s", "Status:", styles.RenderTechnical(info.HelmRelease.Status))
			log.Info().Msgf("  %-15s %s", "Namespace:", styles.RenderTechnical(info.HelmRelease.Namespace))
			log.Info().Msgf("  %-15s %s", "Revision:", styles.RenderTechnical(fmt.Sprintf("%d", info.HelmRelease.Revision)))
			if !info.HelmRelease.LastDeployed.IsZero() {
				log.Info().Msgf("  %-15s %s", "Last Deployed:", styles.RenderTechnical(humanize.Time(info.HelmRelease.LastDeployed)))
			}
		} else {
			log.Info().Msg("No game server deployment found")
		}
		log.Info().Msg("")

		// Image Information
		if info.ImageInfo != nil {
			log.Info().Msg("Image information:")
			log.Info().Msgf("  %-15s %s", "Image tag:", styles.RenderTechnical(info.ImageInfo.ImageTag))
			log.Info().Msgf("  %-15s %s", "Commit ID:", styles.RenderTechnical(info.ImageInfo.CommitID))
			log.Info().Msgf("  %-15s %s", "Build number:", styles.RenderTechnical(info.ImageInfo.BuildNumber))
			log.Info().Msgf("  %-15s %s", "SDK version:", styles.RenderTechnical(info.ImageInfo.SdkVersion))
			log.Info().Msgf("  %-15s %s", "Created:", styles.RenderTechnical(humanize.Time(info.ImageInfo.CreationTime)))
			log.Info().Msg("")
		}

		// Pod Status Information
		if info.PodStatus != nil {
			log.Info().Msg("Pod states:")
			log.Info().Msgf("  Pods: total: %s | ready: %s | running: %s | pending: %s | failed: %s",
				styles.RenderTechnical(fmt.Sprintf("%d", info.PodStatus.TotalPods)),
				styles.RenderTechnical(fmt.Sprintf("%d", info.PodStatus.ReadyPods)),
				styles.RenderTechnical(fmt.Sprintf("%d", info.PodStatus.RunningPods)),
				styles.RenderTechnical(fmt.Sprintf("%d", info.PodStatus.PendingPods)),
				styles.RenderTechnical(fmt.Sprintf("%d", info.PodStatus.FailedPods)))

			// Detailed shard information
			for _, shard := range info.PodStatus.Shards {
				log.Info().Msgf("  Shard: %s (%s pods)", styles.RenderTechnical(shard.ShardSetName),
					styles.RenderTechnical(fmt.Sprintf("%d", shard.Replicas)))
				for _, pod := range shard.Pods {
					readyStatus := "Not Ready"
					if pod.Ready {
						readyStatus = "Ready"
					}
					log.Info().Msgf("    %s: %s | %s | Restarts: %s | Age: %s",
						styles.RenderTechnical(pod.Name), styles.RenderTechnical(pod.Phase), styles.RenderTechnical(readyStatus), styles.RenderTechnical(fmt.Sprintf("%d", pod.Restarts)), styles.RenderTechnical(humanize.Time(time.Now().Add(-pod.Age))))
					if pod.StatusMessage != "" && pod.StatusMessage != pod.Phase {
						log.Info().Msgf("      Status: %s", styles.RenderTechnical(pod.StatusMessage))
					}
				}
			}
		}
		log.Info().Msg("")
	}

	return nil
}

func (o *getServerInfoOpts) gatherDeployedServerInfo(ctx context.Context, targetEnv *envapi.TargetEnvironment, envConfig *metaproj.ProjectEnvironmentConfig) (*serverInfo, error) {
	serverInfo := &serverInfo{}

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

	// Gather Helm release information
	helmInfo, err := o.getHelmReleaseInfo(actionConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get Helm release info: %w", err)
	}
	serverInfo.HelmRelease = helmInfo

	// Gather image information
	imageInfo, err := o.getImageInfo(ctx, targetEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to get image info: %w", err)
	}
	serverInfo.ImageInfo = imageInfo

	// Gather pod status information
	podInfo, err := o.getPodStatusInfo(ctx, targetEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to get pod status info: %w", err)
	}
	serverInfo.PodStatus = podInfo

	return serverInfo, nil
}

func (o *getServerInfoOpts) getHelmReleaseInfo(actionConfig *action.Configuration) (*helmReleaseInfo, error) {
	// Use the constant from deploy_server.go
	existingRelease, err := helmutil.GetExistingRelease(actionConfig, metaplayGameServerChartName)
	if err != nil {
		return nil, fmt.Errorf("failed to get Helm release: %w", err)
	}

	if existingRelease == nil {
		return nil, fmt.Errorf("no game server deployment found")
	}

	helmInfo := &helmReleaseInfo{
		Name:         existingRelease.Name,
		Status:       existingRelease.Info.Status.String(),
		Namespace:    existingRelease.Namespace,
		Revision:     existingRelease.Version,
		LastDeployed: existingRelease.Info.LastDeployed.Time,
	}

	if existingRelease.Chart != nil && existingRelease.Chart.Metadata != nil {
		helmInfo.ChartName = existingRelease.Chart.Metadata.Name
		helmInfo.ChartVersion = existingRelease.Chart.Metadata.Version
	}

	return helmInfo, nil
}

func (o *getServerInfoOpts) getPodStatusInfo(ctx context.Context, targetEnv *envapi.TargetEnvironment) (*podStatusInfo, error) {
	gameServer, err := targetEnv.GetGameServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get game server: %w", err)
	}

	// Get all shard sets with pods
	shardSetsWithPods, err := gameServer.GetAllShardSetsWithPods()
	if err != nil {
		return nil, fmt.Errorf("failed to get shard sets with pods: %w", err)
	}

	// Collect all pods and calculate status in parallel
	var allPods []corev1.Pod
	for _, shardSet := range shardSetsWithPods {
		allPods = append(allPods, shardSet.Pods...)
	}

	// Calculate pod status counts
	totalPods := len(allPods)
	readyPods := 0
	runningPods := 0
	pendingPods := 0
	failedPods := 0

	// Collect results and build shard info
	shardInfos := make([]shardInfo, 0, len(shardSetsWithPods))
	podStatusMap := make(map[string]gameServerPodStatus)

	// Process all pods and resolve their status
	for _, pod := range allPods {
		status := o.resolvePodStatus(pod)
		podStatusMap[pod.Name] = status

		// Count pod statuses
		if status.Ready {
			readyPods++
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			runningPods++
		case corev1.PodPending:
			pendingPods++
		case corev1.PodFailed:
			failedPods++
		}
	}

	// Build shard info with pod details
	for _, shardSet := range shardSetsWithPods {
		podDetails := make([]podDetail, 0, len(shardSet.Pods))
		shardReadyPods := 0
		for _, pod := range shardSet.Pods {
			status := podStatusMap[pod.Name]
			if status.Ready {
				shardReadyPods++
			}
			age := time.Since(pod.CreationTimestamp.Time)
			restartCount := int32(0)
			if len(pod.Status.ContainerStatuses) > 0 {
				restartCount = pod.Status.ContainerStatuses[0].RestartCount
			}

			podDetails = append(podDetails, podDetail{
				Name:          pod.Name,
				Phase:         string(pod.Status.Phase),
				Ready:         status.Ready,
				Restarts:      restartCount,
				Age:           age,
				StatusMessage: status.StatusMessage,
			})
		}

		shardInfos = append(shardInfos, shardInfo{
			ShardSetName:  shardSet.ShardSet.Name,
			Replicas:      int32(len(shardSet.Pods)),
			ReadyReplicas: int32(shardReadyPods),
			Pods:          podDetails,
		})
	}

	return &podStatusInfo{
		TotalPods:   totalPods,
		ReadyPods:   readyPods,
		RunningPods: runningPods,
		PendingPods: pendingPods,
		FailedPods:  failedPods,
		Shards:      shardInfos,
	}, nil
}

// resolvePodStatus determines the pod status and message (local implementation)
func (o *getServerInfoOpts) resolvePodStatus(pod corev1.Pod) gameServerPodStatus {
	// Check if pod is ready
	ready := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			ready = true
			break
		}
	}

	// Determine status message
	statusMessage := string(pod.Status.Phase)
	if pod.Status.Message != "" {
		statusMessage = pod.Status.Message
	} else if len(pod.Status.ContainerStatuses) > 0 {
		containerStatus := pod.Status.ContainerStatuses[0]
		if containerStatus.State.Waiting != nil {
			statusMessage = containerStatus.State.Waiting.Reason
			if containerStatus.State.Waiting.Message != "" {
				statusMessage += ": " + containerStatus.State.Waiting.Message
			}
		} else if containerStatus.State.Terminated != nil {
			statusMessage = containerStatus.State.Terminated.Reason
			if containerStatus.State.Terminated.Message != "" {
				statusMessage += ": " + containerStatus.State.Terminated.Message
			}
		}
	}

	return gameServerPodStatus{
		Ready:         ready,
		StatusMessage: statusMessage,
	}
}

func (o *getServerInfoOpts) getImageInfo(ctx context.Context, targetEnv *envapi.TargetEnvironment) (*imageInfo, error) {
	// Get environment details for ECR repository and credentials
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return nil, fmt.Errorf("failed to get environment details: %w", err)
	}

	// Get game server to access pods
	gameServer, err := targetEnv.GetGameServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get game server: %w", err)
	}

	// Get all shard sets with pods to extract image information
	shardSetsWithPods, err := gameServer.GetAllShardSetsWithPods()
	if err != nil {
		return nil, fmt.Errorf("failed to get shard sets with pods: %w", err)
	}

	// Find the first running pod to extract image information
	var currentImage string
	for _, shardSet := range shardSetsWithPods {
		for _, pod := range shardSet.Pods {
			if pod.Status.Phase == corev1.PodRunning && len(pod.Spec.Containers) > 0 {
				// Get the main container image (usually the first one)
				currentImage = pod.Spec.Containers[0].Image
				break
			}
		}
		if currentImage != "" {
			break
		}
	}

	if currentImage == "" {
		// Fallback: return basic info if no running pods found
		return &imageInfo{
			ImageTag:     "N/A",
			BuildNumber:  "N/A",
			CommitID:     "N/A",
			SdkVersion:   "N/A",
			CreationTime: time.Time{},
		}, nil
	}

	// Extract repository and tag from the full image name
	// Format is typically: <repository>:<tag>
	parts := strings.Split(currentImage, ":")
	tag := "latest"
	if len(parts) >= 2 {
		tag = parts[len(parts)-1]
	}

	// Try to fetch detailed metadata from the remote image
	dockerCredentials, err := targetEnv.GetDockerCredentials(envDetails)
	if err != nil {
		log.Debug().Err(err).Msg("Failed to get Docker credentials, using basic image info")
		return &imageInfo{
			ImageTag:     tag,
			BuildNumber:  "Unable to fetch (no credentials)",
			CommitID:     "Unable to fetch (no credentials)",
			SdkVersion:   "Unable to fetch (no credentials)",
			CreationTime: time.Time{},
		}, nil
	}

	// Fetch detailed metadata from the remote image
	imageMetadata, err := envapi.FetchRemoteDockerImageMetadata(dockerCredentials, currentImage)
	if err != nil {
		log.Debug().Err(err).Msgf("Failed to fetch remote image metadata for %s", currentImage)
		return &imageInfo{
			ImageTag:     tag,
			BuildNumber:  "Unable to fetch metadata",
			CommitID:     "Unable to fetch metadata",
			SdkVersion:   "Unable to fetch metadata",
			CreationTime: time.Time{},
		}, nil
	}

	// Extract information from the metadata
	return &imageInfo{
		ImageTag:     imageMetadata.Tag,
		BuildNumber:  imageMetadata.BuildNumber,
		CommitID:     imageMetadata.CommitID,
		SdkVersion:   imageMetadata.SdkVersion,
		CreationTime: imageMetadata.CreatedTime,
	}, nil
}

func isPodReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
