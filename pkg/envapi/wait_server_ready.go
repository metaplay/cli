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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
}

// Fetch all game server stateful sets (belonging to a particular gameserver deployment).
// \todo If gameServer is specified (only for old operator), only accept statefulsets owned by said gameServer
// \todo For new operator, figure out how to filter them (they currently have no labels)
func fetchGameServerShardSets(ctx context.Context, kubeCli *KubeClient, newGameServer *NewGameServerCR, oldGameServer *OldGameServerCR) ([]appsv1.StatefulSet, error) {
	// Fetch all stateful sets from namespace.
	// log.Debug().Msgf("Fetch game server stateful sets in namespace: %s", namespace)
	statefulSets, err := kubeCli.Clientset.AppsV1().StatefulSets(kubeCli.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=metaplay-server", // \todo only old operator adds this label for now
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
			// No owner, gameserver specified accept all stateful sets
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

// FetchGameServerPods retrieves pods with a specific label selector in a namespace.
// If (optional) shardSets is specified, only return pods owned by said stateful set.
// Otherwise, all pods are returned.
// \todo Figure out how to handle multi-region gameservers
func FetchGameServerPods(ctx context.Context, kubeCli *KubeClient) ([]corev1.Pod, error) {
	log.Debug().Msgf("Fetch game server pods in namespace: %s", kubeCli.Namespace)
	pods, err := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=metaplay-server",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pods: %w", err)
	}
	return pods.Items, nil
}

// gameServerReadiness is the result of one check of whether a game server is up.
type gameServerReadiness struct {
	ready     bool
	lines     []string // Status of each shard set and its pods, for display
	problems  []string // What is not ready yet, one line each
	failedPod string   // Name of a pod whose server failed to start, or empty
	failure   string   // Why failedPod failed
}

// evaluateGameServerReadiness checks that the shard sets run the image the GameServer
// CR asks for and that all their pods are ready. With an empty targetImageTag, any image
// is accepted.
func evaluateGameServerReadiness(targetImageTag string, shardSets []appsv1.StatefulSet, pods []corev1.Pod) gameServerReadiness {
	r := gameServerReadiness{ready: true}
	notReady := func(line string, problem string) {
		r.ready = false
		r.lines = append(r.lines, line)
		r.problems = append(r.problems, problem)
	}

	if len(shardSets) == 0 {
		notReady("Waiting for the operator to create the shard sets", "No shard sets found")
		return r
	}

	for _, shardSet := range shardSets {
		shardSetImage := shardServerImage(shardSet.Spec.Template.Spec)

		// The operator applies a changed GameServer to the StatefulSets some time after
		// Helm has written it. Until then, the StatefulSets and pods are the previous
		// deployment's, and may well be ready.
		if tag := imageTag(shardSetImage); targetImageTag != "" && tag != targetImageTag {
			notReady(
				fmt.Sprintf("Shard set '%s': waiting for the operator to apply the new image", shardSet.Name),
				fmt.Sprintf("Shard set '%s' still runs image %s, not %s", shardSet.Name, tag, targetImageTag))
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
			statuses[ndx] = resolveShardSetPodStatus(&shardSet, shardSetImage, findPod(pods, podNames[ndx]))
			switch statuses[ndx].Phase {
			case PhaseReady:
				numReady++
			case PhaseFailed:
				if r.failedPod == "" {
					r.failedPod = podNames[ndx]
					r.failure = statuses[ndx].Message
				}
			}
		}

		// One line for the shard set, then one per pod with the columns aligned.
		r.lines = append(r.lines, fmt.Sprintf("Shard set '%s': %d/%d pods ready", shardSet.Name, numReady, numReplicas))
		nameWidth := 0
		for _, name := range podNames {
			nameWidth = max(nameWidth, len(name))
		}
		for ndx, status := range statuses {
			r.lines = append(r.lines, strings.TrimRight(fmt.Sprintf("  %-*s  %-8s %s", nameWidth, podNames[ndx], status.Phase, status.Message), " "))
			if status.Phase != PhaseReady {
				r.ready = false
				r.problems = append(r.problems, fmt.Sprintf("Pod %s (%s): %s", podNames[ndx], status.Phase, status.Message))
			}
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
// missing, shutting down, or from an earlier revision of the shard set is pending.
func resolveShardSetPodStatus(shardSet *appsv1.StatefulSet, shardSetImage string, pod *corev1.Pod) GameServerPodStatus {
	if pod == nil {
		return GameServerPodStatus{Phase: PhasePending, Message: "Pod not created yet"}
	}
	if pod.DeletionTimestamp != nil {
		return GameServerPodStatus{Phase: PhasePending, Message: "Previous pod is shutting down"}
	}
	isOwned := slices.ContainsFunc(pod.OwnerReferences, func(ref metav1.OwnerReference) bool {
		return ref.Kind == "StatefulSet" && ref.Name == shardSet.Name
	})
	revision := pod.Labels["controller-revision-hash"]
	if !isOwned || shardServerImage(pod.Spec) != shardSetImage || (revision != "" && revision != shardSet.Status.UpdateRevision) {
		return GameServerPodStatus{Phase: PhasePending, Message: "Previous pod is waiting to be replaced"}
	}
	return resolvePodStatus(*pod)
}

// resolvePodStatus determines the game server pod's phase and status message.
func resolvePodStatus(pod corev1.Pod) GameServerPodStatus {
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
		restarts = fmt.Sprintf(" (restarted %d times)", containerStatus.RestartCount)
	}

	state := containerStatus.State
	switch {
	case state.Running != nil:
		if containerStatus.Ready {
			return GameServerPodStatus{Phase: PhaseReady, Message: strings.TrimSpace(restarts)}
		}
		return GameServerPodStatus{Phase: PhaseRunning, Message: "Server is starting" + restarts}

	case state.Waiting != nil:
		if state.Waiting.Reason == "CrashLoopBackOff" {
			return GameServerPodStatus{
				Phase:   PhaseFailed,
				Message: "Server keeps crashing (CrashLoopBackOff)" + restarts,
			}
		}
		message := "Container is waiting: " + state.Waiting.Reason
		if state.Waiting.Message != "" {
			message += ": " + state.Waiting.Message
		}
		return GameServerPodStatus{Phase: PhasePending, Message: message}

	case state.Terminated != nil:
		return GameServerPodStatus{
			Phase:   PhaseFailed,
			Message: fmt.Sprintf("Server exited: %s (exit code %d)", state.Terminated.Reason, state.Terminated.ExitCode),
		}
	}

	return GameServerPodStatus{Phase: PhaseUnknown, Message: "Container state is unknown"}
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

func findShardServerContainer(pod corev1.Pod) *corev1.ContainerStatus {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == "shard-server" {
			return &status
		}
	}
	return nil
}

// shardServerImage returns the image of the shard-server container, or of the first
// container if none has that name.
func shardServerImage(podSpec corev1.PodSpec) string {
	for _, container := range podSpec.Containers {
		if container.Name == "shard-server" {
			return container.Image
		}
	}
	if len(podSpec.Containers) > 0 {
		return podSpec.Containers[0].Image
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

// targetImageTag returns the image tag that the GameServer CR asks for, or an empty
// string if it cannot be determined.
func (gs *TargetGameServer) targetImageTag() string {
	if gs.GameServerNewCR != nil {
		return gs.GameServerNewCR.Spec.Image.Tag
	}
	if gs.GameServerOldCR != nil {
		return imageTag(shardServerImage(gs.GameServerOldCR.Spec.StatefulSetSpec.Template.Spec))
	}
	return ""
}

// checkGameServerReadiness fetches the game server's StatefulSets and pods and evaluates
// whether they are ready.
// \todo Handle edge clusters of multi-region game servers.
func checkGameServerReadiness(ctx context.Context, kubeCli *KubeClient, gameServer *TargetGameServer, targetImageTag string) (gameServerReadiness, error) {
	shardSets, err := fetchGameServerShardSets(ctx, kubeCli, gameServer.GameServerNewCR, gameServer.GameServerOldCR)
	if err != nil {
		return gameServerReadiness{}, err
	}
	pods, err := FetchGameServerPods(ctx, kubeCli)
	if err != nil {
		return gameServerReadiness{}, err
	}
	return evaluateGameServerReadiness(targetImageTag, shardSets, pods), nil
}

// How often non-interactive mode repeats an unchanged status. Its output goes to an
// append-only log (such as CI), so it is printed when it changes and otherwise only
// this often, to show that the wait is still going on.
const nonInteractiveStatusInterval = 30 * time.Second

// waitForGameServerReady waits until the gameserver in a namespace is ready or a timeout occurs.
func (targetEnv *TargetEnvironment) waitForGameServerReady(ctx context.Context, output *tui.TaskOutput, timeout time.Duration) error {
	// Get target gameServer. It is read after Helm has written it, so it holds the
	// image that is being deployed.
	gameServer, err := targetEnv.GetGameServer(ctx)
	if err != nil {
		return err
	}
	if gameServer.GameServerNewCR == nil && gameServer.GameServerOldCR == nil {
		return clierrors.New("No game server found in the environment")
	}
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}
	targetTag := gameServer.targetImageTag()
	log.Debug().Msgf("Waiting for game server pods with image tag '%s' (new CR: %v)", targetTag, gameServer.GameServerNewCR != nil)

	// Poll more often in interactive mode, where the status is redrawn in place.
	pollInterval := 2 * time.Second
	if tui.IsInteractiveMode() {
		pollInterval = 1 * time.Second
	}

	startTime := time.Now()
	lastShown := ""
	var lastShownAt time.Time
	for {
		readiness, err := checkGameServerReadiness(ctx, kubeCli, gameServer, targetTag)
		if err != nil {
			return clierrors.Wrap(err, "Failed to check the game server status")
		}
		elapsed := time.Since(startTime)

		// Show the status. In non-interactive mode, only when it has changed or has not
		// been shown for a while.
		status := strings.Join(readiness.lines, "\n")
		if tui.IsInteractiveMode() || status != lastShown || time.Since(lastShownAt) >= nonInteractiveStatusInterval {
			header := fmt.Sprintf("Elapsed %s of %s", elapsed.Round(time.Second), timeout)
			if targetTag != "" {
				header = fmt.Sprintf("Image %s, elapsed %s of %s", targetTag, elapsed.Round(time.Second), timeout)
			}
			headerLines := []string{header}
			for _, line := range readiness.lines {
				headerLines = append(headerLines, "  "+line)
			}
			output.SetHeaderLines(headerLines)
			lastShown = status
			lastShownAt = time.Now()
		}

		// If a pod failed, bail out with the logs from the pod.
		if readiness.failedPod != "" {
			podLogs, err := fetchPodLogs(ctx, kubeCli, readiness.failedPod, "shard-server")
			if err != nil {
				output.AppendLinef("Failed to get logs from pod %s: %v", readiness.failedPod, err)
			} else {
				// Route pod logs through TaskOutput footer to avoid writing
				// directly to stdout while Bubble Tea is managing the terminal.
				// Footer lines are not subject to the log line cap.
				logLines := []string{fmt.Sprintf("Logs from pod %s:", readiness.failedPod)}
				for line := range strings.SplitSeq(podLogs, "\n") {
					logLines = append(logLines, fmt.Sprintf("[%s] %s", readiness.failedPod, line))
				}
				output.SetFooterLines(logLines)
			}
			return clierrors.New("Game server failed to start").
				WithDetails(fmt.Sprintf("Pod %s: %s", readiness.failedPod, readiness.failure)).
				WithSuggestion(fmt.Sprintf("Check the pod logs above for details, or run: metaplay debug logs %s", targetEnv.HumanID))
		}

		if readiness.ready {
			return nil
		}

		if elapsed >= timeout {
			return clierrors.Newf("Game server was not ready within %s", timeout).
				WithDetails(readiness.problems...).
				WithSuggestion(fmt.Sprintf("Check the pod states above for details, or the server logs with: metaplay debug logs %s", targetEnv.HumanID))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// fetchPodLogs fetches logs for a specific pod and container.
func fetchPodLogs(ctx context.Context, kubeCli *KubeClient, podName, containerName string) (string, error) {
	log.Debug().Msgf("Fetching logs for pod %s, container %s", podName, containerName)
	var numTailLines int64 = 100
	logOptions := &corev1.PodLogOptions{
		Container: containerName,
		Follow:    false,
		TailLines: &numTailLines,
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
func waitForDomainResolution(output *tui.TaskOutput, hostname string, timeout time.Duration) error {
	timeoutAt := time.Now().Add(timeout)

	output.SetHeaderLines([]string{
		fmt.Sprintf("Waiting for domain %s to resolve (timeout: %s)", hostname, timeout),
	})

	attemptNdx := 0
	for {
		// Do a DNS lookup.
		_, err := net.LookupHost(hostname)
		if err == nil {
			output.AppendLinef("Successfully resolved domain %s", hostname)
			return nil
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
		time.Sleep(5 * time.Second)
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
			return fmt.Errorf("timeout reached while waiting to establish connection to %s:%d", hostname, port)
		default:
			// Require 10 subsequent successful connections to treat the endpoint as healthy.
			const numAttempts = 10
			allSuccess := true
			for iter := range numAttempts {
				// Attempt a connection & bail out on errors.
				err := attemptTLSConnection(hostname, port)
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

			time.Sleep(1 * time.Second) // Wait before retrying
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
func attemptTLSConnection(hostname string, port int) error {
	address := fmt.Sprintf("%s:%d", hostname, port)
	conn, err := tls.Dial("tcp", address, &tls.Config{
		ServerName: hostname,
	})
	if err != nil {
		return fmt.Errorf("TLS connection failed: %w", err)
	}
	defer func() { _ = conn.Close() }()

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

// waitForHTTPServerToRespond pings a target URL until it returns a success status code or a timeout occurs.
func waitForHTTPServerToRespond(ctx context.Context, output *tui.TaskOutput, url string, timeout time.Duration) error {
	timeoutAt := time.Now().Add(timeout)

	output.SetHeaderLines([]string{
		fmt.Sprintf("Waiting for HTTP server %s to respond (timeout: %s)", url, timeout),
	})

	client := &http.Client{
		Timeout: 5 * time.Second, // Per-request timeout
		// Prevent the client from following redirects automatically.
		// We want to check the status code of the initial response directly.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for {
		// Do a request.
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout reached while waiting for %s to respond", url)
		default:
			// Create a new request with headers
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				output.AppendLinef("Error creating request for %s: %v. Retrying...", url, err)
				break
			}

			// Add the Sec-Fetch-Mode header so that StackAPI returns a 302 redirect (instead
			// of 403 forbidden) to the request.
			req.Header.Add("Sec-Fetch-Mode", "navigate")

			// Execute the request
			resp, err := client.Do(req)
			if err != nil {
				output.AppendLinef("Error connecting to %s: %v. Retrying...", url, err)
			} else {
				switch {
				case resp.StatusCode >= 200 && resp.StatusCode < 300:
					// Accept 2xx (Success) status codes.
					_ = resp.Body.Close()
					output.AppendLinef("Successfully connected to %s. Status: %s", url, resp.Status)
					return nil
				case resp.StatusCode >= 300 && resp.StatusCode < 400:
					// Accept 3xx (Redirection) status codes.
					_ = resp.Body.Close()
					output.AppendLinef("Successfully received login redirect from %s. Status: %s", url, resp.Status)
					return nil
				}
				output.AppendLinef("Received status code %d from %s. Retrying...", resp.StatusCode, url)
				_ = resp.Body.Close()
			}
		}

		// Wait before retrying.
		time.Sleep(1 * time.Second)

		// Check for timeout.
		if time.Now().After(timeoutAt) {
			return fmt.Errorf("timeout while waiting for response from %s", url)
		}
	}
}

func (targetEnv *TargetEnvironment) WaitForServerToBeReady(ctx context.Context, taskRunner *tui.TaskRunner) error {
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
		return targetEnv.waitForGameServerReady(ctx, output, 10*time.Minute)
	})

	// CHECK CLIENT-FACING NETWORKING

	serverPrimaryAddress := envDetails.Deployment.ServerHostname
	serverPrimaryPort := 9339 // \todo should use envDetails.Deployment.ServerPorts but its occasionally empty
	log.Debug().Msgf("envDetails.Deployment.ServerPorts: %+v", envDetails.Deployment.ServerPorts)

	// Wait for the primary domain name to resolve to an IP address.
	taskRunner.AddTask("Wait for game server domain name to propagate", func(output *tui.TaskOutput) error {
		return waitForDomainResolution(output, serverPrimaryAddress, 15*time.Minute)
	})

	// Wait for server to respond to client traffic.
	taskRunner.AddTask("Wait for game server to serve clients", func(output *tui.TaskOutput) error {
		return waitForGameServerClientEndpointToBeReady(ctx, output, serverPrimaryAddress, serverPrimaryPort, 5*time.Minute)
	})

	// CHECK ADMIN INTERFACE

	// Wait for the admin domain name to resolve to an IP address.
	taskRunner.AddTask("Wait for LiveOps Dashboard domain name to propagate", func(output *tui.TaskOutput) error {
		return waitForDomainResolution(output, envDetails.Deployment.AdminHostname, 15*time.Minute)
	})

	// Wait for admin API to successfully respond to an HTTP request.
	taskRunner.AddTask("Wait for LiveOps Dashboard to serve traffic", func(output *tui.TaskOutput) error {
		return waitForHTTPServerToRespond(ctx, output, "https://"+envDetails.Deployment.AdminHostname, 5*time.Minute)
	})

	// Success
	return nil
}
