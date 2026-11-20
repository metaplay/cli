/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/rs/zerolog/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// \todo is there an official k8s type for this?
type GameServerPodPhase string

const (
	PhaseReady   GameServerPodPhase = "Ready"
	PhaseRunning GameServerPodPhase = "Running"
	PhasePending GameServerPodPhase = "Pending"
	PhaseUnknown GameServerPodPhase = "Unknown"
	PhaseFailed  GameServerPodPhase = "Failed"
)

// Metaplay wire protocol constants (from SDK WireProtocol.cs)
const (
	metaplayWireProtocolVersion       byte = 10
	protocolStatusClusterRunning      byte = 3
	protocolStatusClusterStarting     byte = 4
	protocolStatusClusterShuttingDown byte = 5
	protocolHeaderSize                     = 8
)

// protocolHeaderInfo holds the parsed fields of the Metaplay protocol header.
type protocolHeaderInfo struct {
	Magic    [4]byte
	Version  byte
	Status   byte
	Reserved [2]byte
}

// buildHealthCheckPacket returns the 8-byte wire protocol HealthCheck request packet.
// Wire packet header: [flags:1][payloadSize:3], where flags bits 0-2 = WirePacketType.
// HealthCheck = 0x04, payload = 4-byte big-endian HealthCheckTypeBits (GlobalState = 0x01).
func buildHealthCheckPacket() []byte {
	return []byte{0x04, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x01}
}

// parseProtocolHeader parses the 8-byte Metaplay protocol header from the given buffer.
func parseProtocolHeader(data []byte) (protocolHeaderInfo, error) {
	if len(data) < protocolHeaderSize {
		return protocolHeaderInfo{}, fmt.Errorf("protocol header too short: got %d bytes, need %d", len(data), protocolHeaderSize)
	}
	var info protocolHeaderInfo
	copy(info.Magic[:], data[0:4])
	info.Version = data[4]
	info.Status = data[5]
	copy(info.Reserved[:], data[6:8])
	return info, nil
}

// validateProtocolHeader checks that the protocol header indicates a healthy server.
func validateProtocolHeader(info protocolHeaderInfo) error {
	if info.Version != metaplayWireProtocolVersion {
		return fmt.Errorf("unexpected protocol version: got %d, expected %d", info.Version, metaplayWireProtocolVersion)
	}
	switch info.Status {
	case protocolStatusClusterRunning:
		return nil
	case protocolStatusClusterStarting:
		return fmt.Errorf("server cluster is still starting (status=%d)", info.Status)
	case protocolStatusClusterShuttingDown:
		return fmt.Errorf("server cluster is shutting down (status=%d)", info.Status)
	default:
		return fmt.Errorf("unexpected protocol status: %d", info.Status)
	}
}

type GameServerPodStatus struct {
	Phase   GameServerPodPhase `json:"phase"`
	Message string             `json:"message"`
	logs    podLogSource       // For a failed pod, which of its logs show the failure
}

// Fetch all game server stateful sets. With the old operator's CR, only those it owns.
// \todo For new operator, figure out how to filter them (they currently have no labels)
func fetchGameServerShardSets(ctx context.Context, kubeCli *KubeClient, oldGameServer *OldGameServerCR) ([]appsv1.StatefulSet, error) {
	// Fetch all stateful sets from namespace.
	// log.Debug().Msgf("Fetch game server stateful sets in namespace: %s", namespace)
	statefulSets, err := kubeCli.Clientset.AppsV1().StatefulSets(kubeCli.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: metaplayGameServerPodLabelSelector, // \todo only old operator adds this label for now
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch stateful sets: %w", err)
	}

	// Filter StatefulSets to include only those owned by gameServer.
	var ownedSets []appsv1.StatefulSet
	for _, sts := range statefulSets.Items {
		log.Debug().Msgf("  StatefulSet: name=%s, currentRevision=%s, updateRevision=%s", sts.GetName(), sts.Status.CurrentRevision, sts.Status.UpdateRevision)

		// If shardset is being terminated, ignore it.
		// \todo show this as shardset getting removed
		if sts.DeletionTimestamp != nil {
			log.Debug().Msgf("    being deleted (deletionTimestamp=%v)", sts.DeletionTimestamp)
			continue
		}

		// If gameserver is provided, check that this is a child of it.
		// Note: Only used with old operator -- new operator does not hold
		// direct ownership due to statefulsets not necessarily living on the
		// same cluster as the gameserver CR.
		// \todo Figure out better revision handling for gameserver->sts relationship
		if oldGameServer != nil {
			for _, ownerRef := range sts.OwnerReferences {
				log.Debug().Msgf("    owner: apiVersion=%s, kind=%s, name=%s, uid=%s", ownerRef.APIVersion, ownerRef.Kind, ownerRef.Name, ownerRef.UID)
				if ownerRef.UID == oldGameServer.Metadata.UID {
					log.Debug().Msgf("      is owned by %s/%s (%s)", ownerRef.Name, ownerRef.Kind, ownerRef.Name)
					ownedSets = append(ownedSets, sts)
					break
				} else {
					log.Debug().Msgf("  Mismatched owner: status.OwnerUID=%s, gameServer.UID=%s", ownerRef.UID, oldGameServer.Metadata.UID)
				}
			}
		} else {
			// Without the old operator's CR, accept all stateful sets
			// \todo Figure out how to handle proper owner & revision
			ownedSets = append(ownedSets, sts)
		}
	}

	// Sort the shard sets by name to ensure consistent order in output.
	slices.SortFunc(ownedSets, func(a, b appsv1.StatefulSet) int {
		return strings.Compare(a.Name, b.Name)
	})

	log.Debug().Msgf("Found %d matching StatefulSets", len(ownedSets))
	return ownedSets, nil
}

// FetchGameServerPods returns all game server pods in the environment's namespace.
// \todo Figure out how to handle multi-region gameservers
func FetchGameServerPods(ctx context.Context, kubeCli *KubeClient) ([]corev1.Pod, error) {
	log.Debug().Msgf("Fetch game server pods in namespace: %s", kubeCli.Namespace)
	pods, err := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: metaplayGameServerPodLabelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pods: %w", err)
	}
	return pods.Items, nil
}

// gameServerReadiness is the result of one check of whether a game server is up.
type gameServerReadiness struct {
	lines         []string     // Status of each shard set and its pods, for display
	problems      []string     // What is not ready yet, one line each, or none when ready
	failedPod     string       // Name of a pod whose server failed to start, or empty
	failure       string       // Why failedPod failed
	failedPodLogs podLogSource // Which logs of failedPod show the failure
}

// podLogSource tells which logs of a pod's server container show its failure.
type podLogSource int

const (
	noPodLogs       podLogSource = iota // The server never started, so it has no logs
	currentPodLogs                      // The current container's
	previousPodLogs                     // The previous container's, as it has been restarted since
)

// ready tells whether everything is ready.
func (r gameServerReadiness) ready() bool {
	return len(r.problems) == 0
}

// readinessTarget is what the game server is expected to run, as declared by its
// GameServer CR, plus the state of the current check: previousPods and now are set
// before each one.
type readinessTarget struct {
	// Image tag of the server. When empty, any image is accepted.
	imageTag string
	// Image tags of the shard sets that override imageTag, by shard set name.
	shardImageTags map[string]string
	// The shard sets the CR declares, mapped to whether they must exist on the primary
	// cluster. Other StatefulSets are ignored. When empty, all StatefulSets found count.
	shardSets map[string]bool
	// Pods from before the deploy that count as previous ones, by UID. See previousPods.
	previousPods map[types.UID]bool
	// The current time, to tell how recently a server was restarted.
	now time.Time
}

// How long the pods from before a deploy that changed the GameServer CR count as
// previous ones, which the operator is about to replace. Until then, they neither make
// the game server ready nor fail the wait. In testing, the operator acted within a few
// seconds of Helm finishing. A change that the operator applies without replacing the
// pods delays the wait by this long. This matters only when the deploy keeps the image
// tag: otherwise, the image check holds back the old pods until they are replaced.
const oldPodGracePeriod = 30 * time.Second

// How recently a pod must have been created for its server's restarts to count as a
// failure to start, rather than as ones in its past.
const recentRestartWindow = 10 * time.Minute

// DeployBaseline is the game server's state just before a deploy changes it. The
// readiness wait uses it to tell the deploy's pods from those before it.
type DeployBaseline struct {
	podUIDs      map[types.UID]bool // Game server pods before the deploy
	crUID        types.UID          // GameServer CR before the deploy, or empty if none
	crGeneration int64              // Generation of that CR
}

// CaptureDeployBaseline records the game server pods and GameServer CR into baseline.
// Call it just before Helm deploys the game server. If it fails, the baseline stays
// empty, which the readiness wait treats as no baseline.
func (targetEnv *TargetEnvironment) CaptureDeployBaseline(ctx context.Context, baseline *DeployBaseline) error {
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}
	pods, err := FetchGameServerPods(ctx, kubeCli)
	if err != nil {
		return err
	}
	// Bypass the cache of GetGameServer, as the CR changes during the deploy. There is
	// none before the first deploy.
	gameServer, err := targetEnv.fetchGameServer(ctx, kubeCli)
	if err != nil && !errors.Is(err, errNoGameServer) {
		return err
	}
	baseline.podUIDs = map[types.UID]bool{}
	for _, pod := range pods {
		baseline.podUIDs[pod.UID] = true
	}
	if gameServer != nil {
		baseline.crUID, baseline.crGeneration = gameServer.crVersion()
	}
	log.Debug().Msgf("Deploy baseline: %d pods, CR UID '%s' generation %d", len(baseline.podUIDs), baseline.crUID, baseline.crGeneration)
	return nil
}

// previousPods returns the pods from before the deploy while the operator may still
// replace them. It does so only if the deploy changed the GameServer CR, and is given
// oldPodGracePeriod to start.
func previousPods(baseline *DeployBaseline, crChanged bool, elapsed time.Duration) map[types.UID]bool {
	if !crChanged || elapsed >= oldPodGracePeriod {
		return nil
	}
	return baseline.podUIDs
}

// evaluateGameServerReadiness checks that the shard sets run the image the GameServer
// CR asks for and that all their pods are ready.
func evaluateGameServerReadiness(target readinessTarget, shardSets []appsv1.StatefulSet, pods []corev1.Pod) gameServerReadiness {
	var r gameServerReadiness
	notReady := func(line string, problem string) {
		r.lines = append(r.lines, line)
		r.problems = append(r.problems, problem)
	}

	// Keep only the declared shard sets, and report the required ones that are missing.
	if len(target.shardSets) > 0 {
		shardSets = slices.DeleteFunc(slices.Clone(shardSets), func(sts appsv1.StatefulSet) bool {
			_, declared := target.shardSets[sts.Name]
			return !declared
		})
		for _, name := range slices.Sorted(maps.Keys(target.shardSets)) {
			isRequired := target.shardSets[name]
			if isRequired && !slices.ContainsFunc(shardSets, func(sts appsv1.StatefulSet) bool { return sts.Name == name }) {
				notReady(
					fmt.Sprintf("Shard set '%s': waiting for the operator to create it", name),
					fmt.Sprintf("Shard set '%s' does not exist", name))
			}
		}
	}
	// A game server needs at least one shard set. When none is required, as all may run
	// on edge clusters, one on the primary cluster is still waited for: it usually runs
	// some, and the operator recreates them during a deploy.
	if len(shardSets) == 0 && len(r.problems) == 0 {
		notReady("Waiting for the operator to create the shard sets", "No shard sets found")
	}

	for _, shardSet := range shardSets {
		shardSetImage := shardServerImage(shardSet.Spec.Template.Spec)

		// The operator applies a changed GameServer to the StatefulSets some time after
		// Helm has written it. Until then, the StatefulSets and pods are the previous
		// deployment's, and may well be ready. An image without a tag (pinned by digest
		// only) cannot be compared, so it is accepted.
		wantTag := target.imageTag
		if shardTag, found := target.shardImageTags[shardSet.Name]; found {
			wantTag = shardTag
		}
		if tag := imageTag(shardSetImage); wantTag != "" && tag != "" && tag != wantTag {
			notReady(
				fmt.Sprintf("Shard set '%s': waiting for the operator to apply the new image", shardSet.Name),
				fmt.Sprintf("Shard set '%s' still runs image %s, not %s", shardSet.Name, tag, wantTag))
			continue
		}

		// Until the StatefulSet controller has seen the latest change to the StatefulSet,
		// its update revision is stale, and pods from the previous revision would match it.
		if shardSet.Status.ObservedGeneration < shardSet.Generation {
			notReady(
				fmt.Sprintf("Shard set '%s': waiting for Kubernetes to apply the change", shardSet.Name),
				fmt.Sprintf("Shard set '%s' has a change that Kubernetes has not applied yet", shardSet.Name))
			continue
		}

		// To update a deployment, the operator first scales the StatefulSets to zero, waits
		// for the pods to stop, and then scales them back up.
		numReplicas := 1
		if shardSet.Spec.Replicas != nil {
			numReplicas = int(*shardSet.Spec.Replicas)
		}
		if numReplicas == 0 {
			notReady(
				fmt.Sprintf("Shard set '%s': stopping the previous deployment", shardSet.Name),
				fmt.Sprintf("Shard set '%s' is still stopping the previous deployment", shardSet.Name))
			continue
		}

		// Resolve the status of each pod.
		podNames := make([]string, numReplicas)
		statuses := make([]GameServerPodStatus, numReplicas)
		numReady := 0
		for ndx := range numReplicas {
			podNames[ndx] = fmt.Sprintf("%s-%d", shardSet.Name, ndx)
			pod := findPod(pods, podNames[ndx])
			isPrevious := pod != nil && target.previousPods[pod.UID]
			status := resolveShardSetPodStatus(&shardSet, shardSetImage, pod, isPrevious, target.now)
			statuses[ndx] = status
			if status.Phase == PhaseReady {
				numReady++
				continue
			}
			r.problems = append(r.problems, fmt.Sprintf("Pod %s (%s): %s", podNames[ndx], status.Phase, status.Message))
			if status.Phase == PhaseFailed && r.failedPod == "" && pod != nil {
				r.failedPod = podNames[ndx]
				r.failure = status.Message
				r.failedPodLogs = status.logs
			}
		}

		// One line for the shard set, then one per pod with the columns aligned. The
		// last pod has the longest name.
		r.lines = append(r.lines, fmt.Sprintf("Shard set '%s': %d/%d pods ready", shardSet.Name, numReady, numReplicas))
		nameWidth := len(podNames[numReplicas-1])
		for ndx, status := range statuses {
			r.lines = append(r.lines, strings.TrimRight(fmt.Sprintf("  %-*s  %-8s %s", nameWidth, podNames[ndx], status.Phase, status.Message), " "))
		}
	}

	return r
}

// findPod returns the pod with the given name, or nil.
func findPod(pods []corev1.Pod, name string) *corev1.Pod {
	for ndx := range pods {
		if pods[ndx].Name == name {
			return &pods[ndx]
		}
	}
	return nil
}

// resolveShardSetPodStatus resolves the status of a shard set's pod. A pod that is
// missing, shutting down, from an earlier revision of the shard set, or isPrevious (from
// before the deploy, see previousPods) is pending.
func resolveShardSetPodStatus(shardSet *appsv1.StatefulSet, shardSetImage string, pod *corev1.Pod, isPrevious bool, now time.Time) GameServerPodStatus {
	if pod == nil {
		return GameServerPodStatus{Phase: PhasePending, Message: "Pod not created yet"}
	}
	if pod.DeletionTimestamp != nil {
		return GameServerPodStatus{Phase: PhasePending, Message: "Previous pod is shutting down"}
	}
	isOwned := slices.ContainsFunc(pod.OwnerReferences, func(ref metav1.OwnerReference) bool {
		// The operator recreates StatefulSets under the same name, so match the UID.
		return ref.Kind == "StatefulSet" && ref.UID == shardSet.UID
	})
	podImage := shardServerImage(pod.Spec)
	revision := pod.Labels["controller-revision-hash"]
	isCurrentRevision := revision == "" || revision == shardSet.Status.UpdateRevision
	if isPrevious || !isOwned || podImage != shardSetImage || !isCurrentRevision {
		log.Debug().Msgf("Pod %s is from a previous deployment: from before the deploy: %v, owned by current StatefulSet: %v, image: %s (StatefulSet: %s), revision: %s (StatefulSet: %s)",
			pod.Name, isPrevious, isOwned, podImage, shardSetImage, revision, shardSet.Status.UpdateRevision)
		return GameServerPodStatus{Phase: PhasePending, Message: "Previous pod is waiting to be replaced"}
	}
	return resolvePodStatus(*pod, now)
}

// resolvePodStatus determines the game server pod's phase and status message.
func resolvePodStatus(pod corev1.Pod, now time.Time) GameServerPodStatus {
	containerStatus := findShardServerContainer(pod)
	if containerStatus == nil {
		// If there is no container created yet, try to resolve the cause
		if pod.Status.Phase == corev1.PodPending {
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
					if condition.Reason == corev1.PodReasonUnschedulable {
						return GameServerPodStatus{
							Phase:   PhasePending,
							Message: "Cannot be scheduled: " + schedulerMessage(condition.Message),
						}
					}
					return GameServerPodStatus{Phase: PhasePending, Message: "Waiting to be scheduled on a node"}
				}
			}
			return GameServerPodStatus{Phase: PhasePending, Message: "Pod is starting"}
		}
		return GameServerPodStatus{Phase: PhaseUnknown, Message: "Server container not found"}
	}

	restarts := ""
	if containerStatus.RestartCount > 0 {
		times := "times"
		if containerStatus.RestartCount == 1 {
			times = "time"
		}
		restarts = fmt.Sprintf(" (restarted %d %s%s)", containerStatus.RestartCount, times, lastExit(containerStatus))
	}

	// A server that is not ready and keeps being restarted fails to start. It may not
	// exit by itself: a server that hangs is killed by its liveness probe, and shows as
	// running between the restarts. One restart is allowed, as a server may exit once
	// while what it depends on is starting too. Kubernetes counts the restarts over the
	// pod's lifetime, so they count only for a recently created pod: in an older one,
	// they may be from the server's past.
	isNewPod := pod.CreationTimestamp.After(now.Add(-recentRestartWindow))
	keepsRestarting := containerStatus.RestartCount >= 2 && isNewPod

	state := containerStatus.State
	switch {
	case state.Running != nil:
		if containerStatus.Ready {
			return GameServerPodStatus{Phase: PhaseReady, Message: strings.TrimSpace(restarts)}
		}
		if keepsRestarting {
			// The previous run is the one that failed.
			return GameServerPodStatus{Phase: PhaseFailed, Message: "Server failed to start" + restarts, logs: previousPodLogs}
		}
		return GameServerPodStatus{Phase: PhaseRunning, Message: "Server is starting" + restarts}

	case state.Waiting != nil:
		if state.Waiting.Reason == "CrashLoopBackOff" {
			// Waits to restart the server that crashed in its previous run.
			return GameServerPodStatus{
				Phase:   PhaseFailed,
				Message: "Server keeps crashing (CrashLoopBackOff)" + restarts,
				logs:    previousPodLogs,
			}
		}
		message := state.Waiting.Reason
		if state.Waiting.Message != "" {
			message += ": " + state.Waiting.Message
		}
		// These do not resolve without a change to the image or the configuration. The
		// container did not start, and an earlier run's logs do not show why.
		if slices.Contains(unrecoverableWaitingReasons, state.Waiting.Reason) {
			return GameServerPodStatus{Phase: PhaseFailed, Message: "Container cannot start: " + message}
		}
		return GameServerPodStatus{Phase: PhasePending, Message: "Container is waiting: " + message}

	case state.Terminated != nil:
		// Kubernetes restarts an exited server, so it fails only when it keeps
		// restarting, as when running.
		exited := fmt.Sprintf("Server exited: %s (exit code %d)", state.Terminated.Reason, state.Terminated.ExitCode)
		if keepsRestarting {
			// Not restarted yet, so the current run is the one that exited.
			return GameServerPodStatus{Phase: PhaseFailed, Message: exited + restarts, logs: currentPodLogs}
		}
		return GameServerPodStatus{Phase: PhasePending, Message: exited + ", restarting" + restarts}
	}

	return GameServerPodStatus{Phase: PhaseUnknown, Message: "Container state is unknown"}
}

// Reasons for a container waiting that Kubernetes does not retry: the image reference is
// invalid, or the pull policy forbids pulling it. Failed pulls (ErrImagePull,
// ImagePullBackOff) and missing secrets or config maps (CreateContainerConfigError) are
// retried and may resolve, so they are left to the timeout.
var unrecoverableWaitingReasons = []string{"InvalidImageName", "ErrImageNeverPull"}

// lastExit describes how the container's previous run ended, such as ", last exit
// code 137 (Error)", or returns an empty string if it is not known.
func lastExit(containerStatus *corev1.ContainerStatus) string {
	terminated := containerStatus.LastTerminationState.Terminated
	if terminated == nil {
		return ""
	}
	return fmt.Sprintf(", last exit code %d (%s)", terminated.ExitCode, terminated.Reason)
}

// schedulerMessage shortens the scheduler's explanation of why a pod cannot be
// scheduled to its first sentence, which says why the nodes did not fit. For example,
// "0/3 nodes are available: 3 Insufficient cpu. preemption: ..." gives
// "0/3 nodes are available: 3 Insufficient cpu."
func schedulerMessage(message string) string {
	if before, _, found := strings.Cut(message, ". "); found {
		return before + "."
	}
	return message
}

// Name of the game server's container in its pods.
const shardServerContainerName = "shard-server"

func findShardServerContainer(pod corev1.Pod) *corev1.ContainerStatus {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == shardServerContainerName {
			return &status
		}
	}
	return nil
}

// shardServerImage returns the image of the shard-server container, or an empty
// string if there is none.
func shardServerImage(podSpec corev1.PodSpec) string {
	for _, container := range podSpec.Containers {
		if container.Name == shardServerContainerName {
			return container.Image
		}
	}
	return ""
}

// imageTag returns the tag of a container image reference, or an empty string if it
// has none. For example, "registry:5000/repo:tag@sha256:..." gives "tag".
func imageTag(image string) string {
	image, _, _ = strings.Cut(image, "@")
	ndx := strings.LastIndex(image, ":")
	if ndx == -1 || strings.Contains(image[ndx:], "/") {
		return ""
	}
	return image[ndx+1:]
}

// readinessTarget returns what the GameServer CR declares.
func (gs *TargetGameServer) readinessTarget() readinessTarget {
	target := readinessTarget{shardSets: map[string]bool{}, shardImageTags: map[string]string{}}
	if cr := gs.GameServerNewCR; cr != nil {
		target.imageTag = cr.Spec.Image.Tag
		// A shard placed by a cluster name or selector may run on an edge cluster,
		// which is not checked, so it need not exist on the primary cluster.
		for _, shard := range cr.Spec.Shards {
			hasClusterName := shard.ClusterName != nil && *shard.ClusterName != ""
			target.shardSets[shard.Name] = !hasClusterName && len(shard.ClusterLabelSelector) == 0
			if shard.Image != nil && shard.Image.Tag != "" {
				target.shardImageTags[shard.Name] = shard.Image.Tag
			}
		}
	}
	if cr := gs.GameServerOldCR; cr != nil {
		target.imageTag = imageTag(shardServerImage(cr.Spec.StatefulSetSpec.Template.Spec))
		for _, shard := range cr.Spec.ShardSpec {
			target.shardSets[shard.Name] = true
		}
	}
	return target
}

// pause waits for d between two attempts of a wait. It returns false at once when ctx is done.
func pause(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// crVersion returns the UID and generation of the game server's CR.
func (gs *TargetGameServer) crVersion() (types.UID, int64) {
	if cr := gs.GameServerNewCR; cr != nil {
		return cr.UID, cr.Generation
	}
	if cr := gs.GameServerOldCR; cr != nil {
		return cr.Metadata.UID, cr.Metadata.Generation
	}
	return "", 0
}

// checkGameServerReadiness fetches the game server's StatefulSets and pods and evaluates
// whether they are ready.
// \todo Handle edge clusters of multi-region game servers.
func checkGameServerReadiness(ctx context.Context, kubeCli *KubeClient, gameServer *TargetGameServer, target readinessTarget) (gameServerReadiness, error) {
	shardSets, err := fetchGameServerShardSets(ctx, kubeCli, gameServer.GameServerOldCR)
	if err != nil {
		return gameServerReadiness{}, err
	}
	pods, err := FetchGameServerPods(ctx, kubeCli)
	if err != nil {
		return gameServerReadiness{}, err
	}
	return evaluateGameServerReadiness(target, shardSets, pods), nil
}

// How often an unchanged status is repeated when it is logged: in non-interactive mode,
// or once the display has closed. The log is append-only (such as in CI), so the status
// is printed when it changes and otherwise only this often, to show that the wait is
// still going on.
const loggedStatusInterval = 30 * time.Second

// How long checks of the game server status may keep failing, for example on a
// transient Kubernetes API error, before the wait gives up.
const maxFailingChecksDuration = 15 * time.Second

// waitForGameServerReady waits until the gameserver in a namespace is ready or a timeout
// occurs. See WaitForServerToBeReady for baseline.
func (targetEnv *TargetEnvironment) waitForGameServerReady(ctx context.Context, output *tui.TaskOutput, timeout time.Duration, baseline *DeployBaseline) error {
	startTime := time.Now()
	stopped := func() error {
		return clierrors.Wrap(ctx.Err(), "Stopped waiting for the game server to be ready")
	}
	waitForNextPoll := func() error {
		// Poll more often when the status is redrawn in place than when it is logged.
		pollInterval := 1 * time.Second
		if output.LogsLines() {
			pollInterval = 2 * time.Second
		}
		if !pause(ctx, pollInterval) {
			return stopped()
		}
		return nil
	}
	// A failed Kubernetes API call is retried, until calls have failed for
	// maxFailingChecksDuration. One fails also when the wait is cancelled.
	lastSuccessAt := startTime
	retryFailedCheck := func(err error, failure string) error {
		if ctx.Err() != nil {
			return stopped()
		}
		if errors.Is(err, errNoGameServer) {
			return err
		}
		isPermanent := apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err)
		if isPermanent || time.Since(lastSuccessAt) >= maxFailingChecksDuration {
			return clierrors.Wrap(err, failure)
		}
		output.AppendLinef("%s, retrying: %v", failure, err)
		return waitForNextPoll()
	}

	// Read the game server's CR, which holds the image being deployed. Bypass the cache
	// of GetGameServer, which may hold the CR from before Helm wrote it.
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}
	gameServer, err := targetEnv.fetchGameServer(ctx, kubeCli)
	for err != nil {
		if err := retryFailedCheck(err, "Failed to read the GameServer resource"); err != nil {
			return err
		}
		gameServer, err = targetEnv.fetchGameServer(ctx, kubeCli)
	}
	lastSuccessAt = time.Now()
	target := gameServer.readinessTarget()
	crUID, crGeneration := gameServer.crVersion()
	crChanged := baseline.crUID != crUID || baseline.crGeneration != crGeneration
	log.Debug().Msgf("Waiting for game server: %+v (new CR: %v, CR changed by the deploy: %v)", target, gameServer.GameServerNewCR != nil, crChanged)

	notReadyError := func(problems []string) error {
		return clierrors.Newf("Game server was not ready within %s", timeout).
			WithDetails(problems...).
			WithSuggestion(fmt.Sprintf("Check the pod states above for details, or the server logs with: metaplay debug logs %s", targetEnv.HumanID))
	}
	headerPrefix := "Elapsed"
	if target.imageTag != "" {
		headerPrefix = fmt.Sprintf("Image %s, elapsed", target.imageTag)
	}
	lastShown := ""
	var lastShownAt time.Time
	var lastProblems []string // Of the last successful check
	for {
		target.now = time.Now()
		target.previousPods = previousPods(baseline, crChanged, time.Since(startTime))
		readiness, err := checkGameServerReadiness(ctx, kubeCli, gameServer, target)
		elapsed := time.Since(startTime)
		if err != nil {
			// At the deadline, what was not ready tells more than the failed check.
			if elapsed >= timeout && lastProblems != nil && ctx.Err() == nil {
				return notReadyError(lastProblems)
			}
			if err := retryFailedCheck(err, "Failed to check the game server status"); err != nil {
				return err
			}
			continue
		}
		lastSuccessAt = time.Now()
		lastProblems = readiness.problems

		// Show the status. When it is logged (in non-interactive mode, or after the
		// display has closed), only when it has changed or has not been shown for a while.
		status := strings.Join(readiness.lines, "\n")
		if !output.LogsLines() || status != lastShown || time.Since(lastShownAt) >= loggedStatusInterval {
			headerLines := []string{fmt.Sprintf("%s %s of %s", headerPrefix, elapsed.Round(time.Second), timeout)}
			for _, line := range readiness.lines {
				headerLines = append(headerLines, "  "+line)
			}
			output.SetHeaderLines(headerLines)
			lastShown = status
			lastShownAt = time.Now()
		}

		// If a pod failed, bail out with the logs from the pod.
		if readiness.failedPod != "" {
			showPodLogs(ctx, output, kubeCli, readiness.failedPod, readiness.failedPodLogs)
			suggestion := fmt.Sprintf("Check the pod logs above for details, or run: metaplay debug logs %s", targetEnv.HumanID)
			if readiness.failedPodLogs == noPodLogs {
				suggestion = "The server container did not start, so it has no logs. Fix the cause above, and deploy again"
			}
			return clierrors.New("Game server failed to start").
				WithDetails(fmt.Sprintf("Pod %s: %s", readiness.failedPod, readiness.failure)).
				WithSuggestion(suggestion)
		}

		if readiness.ready() {
			return nil
		}

		if elapsed >= timeout {
			return notReadyError(readiness.problems)
		}

		if err := waitForNextPoll(); err != nil {
			return err
		}
	}
}

// showPodLogs shows the last logs of a game server pod's server container that show its
// failure in the task output, or why they could not be fetched.
func showPodLogs(ctx context.Context, output *tui.TaskOutput, kubeCli *KubeClient, podName string, source podLogSource) {
	if source == noPodLogs {
		return
	}
	podLogs, err := fetchPodLogs(ctx, kubeCli, podName, shardServerContainerName, source == previousPodLogs)
	// The previous container's logs are gone if Kubernetes has cleaned the container up.
	// The current container's logs may show the same failure in progress.
	if err != nil && source == previousPodLogs {
		output.AppendLinef("Failed to get the logs of the previous run of pod %s, showing the current run: %v", podName, err)
		podLogs, err = fetchPodLogs(ctx, kubeCli, podName, shardServerContainerName, false)
	}
	if err != nil {
		output.AppendLinef("Failed to get logs from pod %s: %v", podName, err)
		return
	}
	// Route pod logs through TaskOutput footer to avoid writing directly to stdout while
	// Bubble Tea is managing the terminal. Footer lines are not subject to the log line cap.
	logLines := []string{fmt.Sprintf("Logs from pod %s:", podName)}
	for line := range strings.SplitSeq(podLogs, "\n") {
		logLines = append(logLines, fmt.Sprintf("[%s] %s", podName, line))
	}
	output.SetFooterLines(logLines)
}

// fetchPodLogs fetches logs for a specific pod and container. With previous, it fetches
// the logs of the container's previous run.
func fetchPodLogs(ctx context.Context, kubeCli *KubeClient, podName, containerName string, previous bool) (string, error) {
	log.Debug().Msgf("Fetching logs for pod %s, container %s (previous: %v)", podName, containerName, previous)
	var numTailLines int64 = 100
	logOptions := &corev1.PodLogOptions{
		Container: containerName,
		Follow:    false,
		TailLines: &numTailLines,
		Previous:  previous,
	}

	req := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).GetLogs(podName, logOptions)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get pod logs: %w", err)
	}
	defer func() { _ = stream.Close() }()

	builder := &strings.Builder{}
	_, err = io.Copy(builder, stream)
	if err != nil {
		return "", fmt.Errorf("failed to read pod logs: %w", err)
	}

	return builder.String(), nil
}

// waitForDomainResolution waits for a domain to resolve within a 15-minute timeout.
func waitForDomainResolution(ctx context.Context, output *tui.TaskOutput, hostname string, timeout time.Duration) error {
	timeoutAt := time.Now().Add(timeout)

	output.SetHeaderLines([]string{
		fmt.Sprintf("Waiting for domain %s to resolve (timeout: %s)", hostname, timeout),
	})

	attemptNdx := 0
	for {
		// Do a DNS lookup.
		_, err := net.DefaultResolver.LookupHost(ctx, hostname)
		if err == nil {
			output.AppendLinef("Successfully resolved domain %s", hostname)
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("stopped waiting for domain %s to resolve: %w", hostname, ctx.Err())
		}

		// Check for timeout.
		if time.Now().After(timeoutAt) {
			return fmt.Errorf("could not resolve domain %s before timeout", hostname)
		}

		if dnsErr, ok := errors.AsType[*net.DNSError](err); ok && dnsErr.IsNotFound {
			output.AppendLinef("Attempt %d failed: %v", attemptNdx+1, dnsErr)
		} else {
			output.AppendLinef("Failed to resolve %s: %v. Retrying...", hostname, err)
		}

		attemptNdx += 1

		// Delay before trying again -- these can take a while so avoid spamming the log
		if !pause(ctx, 5*time.Second) {
			return fmt.Errorf("stopped waiting for domain %s to resolve: %w", hostname, ctx.Err())
		}
	}
}

// waitForGameServerClientEndpointToBeReady waits until a game server client endpoint is ready by performing a TLS handshake.
func waitForGameServerClientEndpointToBeReady(ctx context.Context, output *tui.TaskOutput, hostname string, port int, timeout time.Duration) error {
	timeoutAt := time.Now().Add(timeout)

	output.SetHeaderLines([]string{
		fmt.Sprintf("Waiting for game server endpoint %s:%d to be ready (timeout: %s)", hostname, port, timeout),
	})

	for {
		// Do a request.
		select {
		case <-ctx.Done():
			return fmt.Errorf("stopped waiting to establish connection to %s:%d: %w", hostname, port, ctx.Err())
		default:
			// Require 10 subsequent successful connections to treat the endpoint as healthy.
			const numAttempts = 10
			allSuccess := true
			for iter := range numAttempts {
				// Attempt a connection & bail out on errors.
				err := attemptTLSConnection(ctx, hostname, port)
				if ctx.Err() != nil {
					return fmt.Errorf("stopped waiting to establish connection to %s:%d: %w", hostname, port, ctx.Err())
				}
				if err != nil {
					output.AppendLinef("Connection attempt %d of %d failed: %v", iter+1, numAttempts, err)
					allSuccess = false
					break
				}
			}

			// If all attempt succeeded, we're done.
			if allSuccess {
				output.AppendLinef("Successfully connected to the target environment %s:%d", hostname, port)
				return nil
			}

			// Wait before retrying
			if !pause(ctx, 1*time.Second) {
				return fmt.Errorf("stopped waiting to establish connection to %s:%d: %w", hostname, port, ctx.Err())
			}
		}

		// Check for timeout.
		if time.Now().After(timeoutAt) {
			return fmt.Errorf("timeout while waiting for response from %s:%d", hostname, port)
		}
	}
}

// attemptTLSConnection performs a TLS handshake, sends a HealthCheck packet
// (client-speaks-first pattern to work behind TLS-terminating proxies), then
// reads and validates the server's protocol header.
func attemptTLSConnection(ctx context.Context, hostname string, port int) error {
	address := fmt.Sprintf("%s:%d", hostname, port)
	dialer := &tls.Dialer{Config: &tls.Config{ServerName: hostname}}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("TLS connection failed: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stopClosing := context.AfterFunc(ctx, func() { _ = conn.Close() }) // Ends a read or write on cancel
	defer stopClosing()

	// Send HealthCheck packet to trigger the upstream connection in TLS-terminating
	// proxies that use lazy upstream connections (client-speaks-first pattern).
	log.Debug().Msgf("TLS handshake completed, sending HealthCheck packet...")
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	healthCheckPacket := buildHealthCheckPacket()
	if _, err := conn.Write(healthCheckPacket); err != nil {
		return fmt.Errorf("failed to send HealthCheck packet: %w", err)
	}

	// Read the protocol header from the server.
	log.Debug().Msgf("HealthCheck sent, waiting to receive protocol header from server...")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buffer := make([]byte, protocolHeaderSize)
	totalRead := 0
	for totalRead < protocolHeaderSize {
		n, err := conn.Read(buffer[totalRead:])
		if err != nil {
			return fmt.Errorf("error reading protocol header from server (got %d/%d bytes): %w", totalRead, protocolHeaderSize, err)
		}
		totalRead += n
	}

	// Log received bytes for debugging.
	hexBytes := make([]string, totalRead)
	for i := range totalRead {
		hexBytes[i] = fmt.Sprintf("%02x", buffer[i])
	}
	log.Debug().Msgf("Received %d bytes from server: %s", totalRead, hexBytes)

	// Parse and validate the protocol header.
	header, err := parseProtocolHeader(buffer[:totalRead])
	if err != nil {
		return fmt.Errorf("failed to parse protocol header: %w", err)
	}
	log.Debug().Msgf("Protocol header: version=%d, status=%d, magic=%x", header.Version, header.Status, header.Magic)

	if err := validateProtocolHeader(header); err != nil {
		return fmt.Errorf("protocol header validation failed: %w", err)
	}

	return nil
}

// The port of the game server's client endpoint.
// \todo should use envDetails.Deployment.ServerPorts but its occasionally empty
const gameServerClientPort = 9339

// probeHTTPServer makes one GET request to url. A success or a redirect status passes:
// the LiveOps Dashboard redirects to its login. It returns the status of the response,
// if there was one, and an error unless it passed.
func probeHTTPServer(ctx context.Context, url string) (string, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		// Check the status of the response itself, not of where it redirects to.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	// With this header, StackAPI responds with a 302 redirect instead of 403 Forbidden.
	req.Header.Add("Sec-Fetch-Mode", "navigate")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return resp.Status, fmt.Errorf("received status %s", resp.Status)
	}
	return resp.Status, nil
}

// waitForHTTPServerToRespond pings a target URL until it returns a success status code or a timeout occurs.
func waitForHTTPServerToRespond(ctx context.Context, output *tui.TaskOutput, url string, timeout time.Duration) error {
	timeoutAt := time.Now().Add(timeout)

	output.SetHeaderLines([]string{
		fmt.Sprintf("Waiting for HTTP server %s to respond (timeout: %s)", url, timeout),
	})

	for {
		status, err := probeHTTPServer(ctx, url)
		if ctx.Err() != nil {
			return fmt.Errorf("stopped waiting for %s to respond: %w", url, ctx.Err())
		}
		if err == nil {
			output.AppendLinef("Successfully connected to %s. Status: %s", url, status)
			return nil
		}
		output.AppendLinef("Request to %s failed: %v. Retrying...", url, err)

		// Wait before retrying.
		if !pause(ctx, 1*time.Second) {
			return fmt.Errorf("stopped waiting for %s to respond: %w", url, ctx.Err())
		}

		// Check for timeout.
		if time.Now().After(timeoutAt) {
			return fmt.Errorf("timeout while waiting for response from %s", url)
		}
	}
}

// WaitForServerToBeReady adds the tasks that wait for the game server to serve clients
// and the LiveOps Dashboard after a deploy. baseline is the state before the deploy,
// which CaptureDeployBaseline fills in when the deploy's tasks run. A failing pod from
// before the deploy then fails the wait only once the operator has had time to replace
// it. CheckServerStatus makes the same checks once, without waiting.
func (targetEnv *TargetEnvironment) WaitForServerToBeReady(ctx context.Context, taskRunner *tui.TaskRunner, baseline *DeployBaseline) error {
	// Fetch environment details.
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return err
	}

	// Wait for the gameserver Kubernetes resources to be ready.
	// Only wait for a few minutes as pods generally become healthy fairly
	// soon as we want to display the logs from errors early.
	// This can take a long time when larger changes are being applied (eg,
	// enabling the new operator).
	taskRunner.AddTask("Wait for game server pods to be ready", func(output *tui.TaskOutput) error {
		return targetEnv.waitForGameServerReady(ctx, output, 10*time.Minute, baseline)
	})

	// CHECK CLIENT-FACING NETWORKING

	serverPrimaryAddress := envDetails.Deployment.ServerHostname
	log.Debug().Msgf("envDetails.Deployment.ServerPorts: %+v", envDetails.Deployment.ServerPorts)

	// Wait for the primary domain name to resolve to an IP address.
	taskRunner.AddTask("Wait for game server domain name to propagate", func(output *tui.TaskOutput) error {
		return waitForDomainResolution(ctx, output, serverPrimaryAddress, 15*time.Minute)
	})

	// Wait for server to respond to client traffic.
	taskRunner.AddTask("Wait for game server to serve clients", func(output *tui.TaskOutput) error {
		return waitForGameServerClientEndpointToBeReady(ctx, output, serverPrimaryAddress, gameServerClientPort, 5*time.Minute)
	})

	// CHECK ADMIN INTERFACE

	// Wait for the admin domain name to resolve to an IP address.
	taskRunner.AddTask("Wait for LiveOps Dashboard domain name to propagate", func(output *tui.TaskOutput) error {
		return waitForDomainResolution(ctx, output, envDetails.Deployment.AdminHostname, 15*time.Minute)
	})

	// Wait for admin API to successfully respond to an HTTP request.
	taskRunner.AddTask("Wait for LiveOps Dashboard to serve traffic", func(output *tui.TaskOutput) error {
		return waitForHTTPServerToRespond(ctx, output, "https://"+envDetails.Deployment.AdminHostname, 5*time.Minute)
	})

	// Success
	return nil
}
