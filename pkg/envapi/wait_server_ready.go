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
	"strings"
	"time"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const metaplayGameServerChartName = "metaplay-gameserver"

// \todo is there an official k8s type for this?
type GameServerPodPhase string

const (
	PhaseReady   GameServerPodPhase = "Ready"
	PhaseRunning GameServerPodPhase = "Running"
	PhasePending GameServerPodPhase = "Pending"
	PhaseUnknown GameServerPodPhase = "Unknown"
	PhaseFailed  GameServerPodPhase = "Failed"
)

type GameServerPodStatus struct {
	Phase   GameServerPodPhase `json:"phase"`
	Message string             `json:"message"`
	Details interface{}        `json:"details,omitempty"`
}

// Fetch all game server stateful sets (belonging to a particular gameserver deployment).
// \todo If gameServer is specified (only for old operator), only accept statefulsets owned by said gameServer
// \todo For new operator, figure out how to filter them (they currently have no labels)
func fetchGameServerShardSets(ctx context.Context, kubeCli *KubeClient, gameServer *OldGameServerCR) ([]appsv1.StatefulSet, error) {
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

		// If gameserver is provided, check that this is a child of it.
		// Note: Only used with old operator -- new operator does not hold
		// direct ownership due to statefulsets not necessarily living on the
		// same cluster as the gameserver CR.
		// \todo Figure out better revision handling for gameserver->sts relationship
		if gameServer != nil {
			for _, ownerRef := range sts.OwnerReferences {
				log.Debug().Msgf("    owner: apiVersion=%s, kind=%s, name=%s, uid=%s", ownerRef.APIVersion, ownerRef.Kind, ownerRef.Name, ownerRef.UID)
				if ownerRef.UID == gameServer.Metadata.UID {
					log.Debug().Msgf("      is owned by %s/%s (%s)", ownerRef.Name, ownerRef.Kind, ownerRef.Name)
					ownedSets = append(ownedSets, sts)
					break
				} else {
					log.Debug().Msgf("  Mismatched owner: status.OwnerUID=%s, gameServer.UID=%s", ownerRef.UID, gameServer.Metadata.UID)
				}
			}
		} else {
			// No owner, gameserver specified accept all stateful sets
			// \todo Figure out how to handle proper owner & revision
			ownedSets = append(ownedSets, sts)
		}
	}

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

// Fetch all game server pods from a namespace for the given shardSets.
// Return a map of shardName->[]pods. The per-shard arrays are of the length of
// the expected pods in each shard and can contain nil values (for missing pods).
func fetchGameServerPodsByShardSet(ctx context.Context, kubeCli *KubeClient, shardSets []appsv1.StatefulSet) (map[string][]*corev1.Pod, error) {
	// Fetch all gameserver pods in the namespace
	pods, err := FetchGameServerPods(ctx, kubeCli)
	if err != nil {
		return nil, err
	}

	// Resolve the pods for each shard set.
	result := make(map[string][]*corev1.Pod, len(shardSets))

	// Check that all expected pods from StatefulSets exist.
	for _, shardSet := range shardSets {
		numExpectedReplicas := int(*shardSet.Spec.Replicas)
		log.Debug().Msgf("StatefulSet '%s': expecting %d pod(s)", shardSet.Name, numExpectedReplicas)

		// Allocate a state for each expected pod in the stateful set.
		shardPods := make([]*corev1.Pod, numExpectedReplicas)
		result[shardSet.Name] = shardPods

		// Check that all expected pods are found.
		for shardNdx := 0; shardNdx < numExpectedReplicas; shardNdx++ {
			// Find matching pod with name '<shardSet>-<index>'
			podName := fmt.Sprintf("%s-%d", shardSet.Name, shardNdx)
			var foundPod *corev1.Pod = nil
			for _, pod := range pods {
				if pod.Name == podName {
					// Check if pod belongs to this StatefulSet through owner references
					belongsToStatefulSet := false
					for _, ownerRef := range pod.OwnerReferences {
						if ownerRef.Kind == "StatefulSet" && ownerRef.Name == shardSet.Name {
							belongsToStatefulSet = true
							break
						}
					}
					if !belongsToStatefulSet {
						log.Debug().Msgf("Pod %s does not belong to StatefulSet", pod.Name)
						continue
					}

					// Verify pod has matching docker image version
					if len(pod.Spec.Containers) == 0 {
						continue
					}
					podImage := pod.Spec.Containers[0].Image
					statefulSetImage := shardSet.Spec.Template.Spec.Containers[0].Image
					if podImage != statefulSetImage {
						log.Debug().Msgf("Pod %s has mismatched image version. Expected %s, got %s", pod.Name, statefulSetImage, podImage)
						continue
					}

					// Check pod is not terminating
					if pod.DeletionTimestamp != nil {
						log.Debug().Msgf("Pod %s is being terminated", pod.Name)
						continue
					}

					// Check generation matches
					if pod.Labels["controller-revision-hash"] != "" &&
						pod.Labels["controller-revision-hash"] != shardSet.Status.UpdateRevision {
						log.Debug().Msgf("Pod %s has outdated revision hash", pod.Name)
						continue
					}

					foundPod = &pod
				}
			}

			// Store pod (found or not).
			shardPods[shardNdx] = foundPod
		}
	}

	return result, nil
}

// resolvePodStatus determines the game server pod's phase and status message.
func resolvePodStatus(pod corev1.Pod) GameServerPodStatus {
	if pod.Status.ContainerStatuses == nil || len(pod.Status.ContainerStatuses) == 0 {
		return GameServerPodStatus{
			Phase:   PhaseUnknown,
			Message: "ContainerStatuses is empty",
		}
	}

	containerStatus := findShardServerContainer(pod)
	if containerStatus == nil {
		return GameServerPodStatus{
			Phase:   PhaseUnknown,
			Message: "Shard server container not found",
		}
	}

	// Enable for really detailed status logging
	// log.Debug().Msgf("Pod %s container status: %+v", pod.Name, containerStatus)
	state := containerStatus.State

	switch {
	case state.Running != nil:
		if containerStatus.Ready {
			return GameServerPodStatus{
				Phase:   PhaseReady,
				Message: fmt.Sprintf("Container %s is ready", containerStatus.Name),
				Details: state.Running,
			}
		}
		return GameServerPodStatus{
			Phase:   PhaseRunning,
			Message: fmt.Sprintf("Container %s is running but not ready", containerStatus.Name),
			Details: state.Running,
		}

	case state.Waiting != nil:
		return GameServerPodStatus{
			Phase:   PhasePending,
			Message: fmt.Sprintf("Container %s is waiting: %s", containerStatus.Name, state.Waiting.Reason),
			Details: state.Waiting,
		}

	case state.Terminated != nil:
		return GameServerPodStatus{
			Phase:   PhaseFailed,
			Message: fmt.Sprintf("Container %s is terminated: %s", containerStatus.Name, state.Terminated.Reason),
			Details: state.Terminated,
		}
	}

	return GameServerPodStatus{
		Phase:   PhaseUnknown,
		Message: "Container state is unknown",
	}
}

func findShardServerContainer(pod corev1.Pod) *corev1.ContainerStatus {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == "shard-server" {
			return &status
		}
	}
	return nil
}

// Check if a gameserver CR is ready. Looks at the gameserver CR's .status.phase.
// The stateful sets and pods can live on other clusters (in other regions) so we
// don't have direct access to them (at least for now).
func isGameServerCRReady(gameServer *NewGameServerCR) (bool, error) {
	log.Debug().Msgf("New gameserver CR status.phase = %s", gameServer.Status.Phase)
	// \todo check statefulset & pod statuses as well?
	return gameServer.Status.Phase == "Running", nil
}

// Check if the given gameserver CR is ready.
// Only works with the old gameserver CRs (for now anyway).
// \todo Provide more detailed output as to what the status is -- to be used in various diagnostics
// \todo Consider using this with new operator as well: requires multi-region handling & proper CR<->sts ownership/revision relationships
func isOldGameServerReady(ctx context.Context, kubeCli *KubeClient, gameServer *OldGameServerCR) (bool, error) {
	// Fetch all game server StatefulSets owned by the game server.
	// \todo this only works in single-region setups .. use only with old operator?
	shardSets, err := fetchGameServerShardSets(ctx, kubeCli, gameServer)
	if err != nil {
		return false, err
	}

	// If no matching StatefulSets, server is not ready.
	if len(shardSets) == 0 {
		return false, nil
	}

	// Fetch all the game server pods in the namespace.
	podsByShard, err := fetchGameServerPodsByShardSet(ctx, kubeCli, shardSets)
	if err != nil {
		return false, err
	}

	// Check that all pods belonging to all shards are ready.
	// \todo Parallelize this -- checking lots of pods sequentially is slow
	allReady := true
	for shardSetName, shardSetPods := range podsByShard {
		// Check that all expected pods are found.
		log.Debug().Msgf("ShardSet '%s' pods (%d):", shardSetName, len(shardSetPods))
		for podNdx, pod := range shardSetPods {
			// Check that the pod is healthy & ready.
			podName := fmt.Sprintf("%s-%d", shardSetName, podNdx)
			if pod != nil {
				status := resolvePodStatus(*pod)
				log.Debug().Msgf("  %s: %s [%s]", podName, status.Phase, status.Message)
				if status.Phase != PhaseReady {
					allReady = false
				}
			} else {
				log.Debug().Msgf("  %s: not found", podName)
			}
		}
	}

	// All pods in all shard sets are ready!
	return allReady, nil
}

// waitForGameServerReady waits until the gameserver in a namespace is ready or a timeout occurs.
func (targetEnv *TargetEnvironment) waitForGameServerReady(ctx context.Context, timeout time.Duration) error {
	// Get target gameServer.
	gameServer, err := targetEnv.GetGameServer(ctx)
	if err != nil {
		return err
	}

	// Keep checking the gameservers until they are ready, or timeout is hit.
	startTime := time.Now()
	for time.Since(startTime) < timeout {
		var isReady bool
		var err error

		// Try to get the gameserver CR used by the new operator.
		if gameServer.GameServerNewCR != nil {
			log.Debug().Msgf("Check if new gameserver CR is ready")
			isReady, err = isGameServerCRReady(gameServer.GameServerNewCR)
			if err != nil {
				return err
			}
		} else if gameServer.GameServerOldCR != nil {
			log.Debug().Msgf("Check if old gameserver CR is ready")
			kubeCli, err := targetEnv.GetPrimaryKubeClient()
			if err != nil {
				return err
			}
			isReady, err = isOldGameServerReady(ctx, kubeCli, gameServer.GameServerOldCR)
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("no old or new gameserver CR found")
		}

		// If gamserver is ready, we're done.
		if isReady {
			return nil
		}

		// Wait a bit to check again.
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("timeout waiting for pods to be ready")
}

// fetchPodLogs fetches logs for a specific pod and container.
func fetchPodLogs(ctx context.Context, kubeCli *KubeClient, podName, containerName string) (string, error) {
	log.Debug().Msgf("Fetching logs for pod %s, container %s", podName, containerName)
	logOptions := &corev1.PodLogOptions{
		Container: containerName,
		Follow:    false,
		TailLines: int64Ptr(100),
	}

	req := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).GetLogs(podName, logOptions)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get pod logs: %w", err)
	}
	defer stream.Close()

	builder := &strings.Builder{}
	_, err = io.Copy(builder, stream)
	if err != nil {
		return "", fmt.Errorf("failed to read pod logs: %w", err)
	}

	return builder.String(), nil
}

func int64Ptr(i int64) *int64 { return &i }

// waitForDomainResolution waits for a domain to resolve within a 15-minute timeout.
func waitForDomainResolution(hostname string, timeout time.Duration) error {
	timeoutAt := time.Now().Add(timeout)

	for {
		_, err := net.LookupHost(hostname)
		if err == nil {
			log.Debug().Msgf("Successfully resolved domain %s", hostname)
			return nil
		}

		if time.Now().After(timeoutAt) {
			return fmt.Errorf("could not resolve domain %s before timeout", hostname)
		}

		if dnsErr, ok := err.(*net.DNSError); ok && dnsErr.IsNotFound {
			log.Debug().Msgf("Waiting for domain name %s to propagate... This can take up to 15 minutes on the first deploy.", styles.RenderTechnical(hostname))
		} else {
			log.Debug().Msgf("Failed to resolve %s: %v. Retrying...", hostname, err)
		}

		// Delay before trying again -- these can take a while so avoid spamming the log
		time.Sleep(5 * time.Second)
	}
}

// waitForGameServerClientEndpointToBeReady waits until a game server client endpoint is ready by performing a TLS handshake.
func waitForGameServerClientEndpointToBeReady(ctx context.Context, hostname string, port int, timeout time.Duration) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout reached while waiting to establish connection to %s:%d", hostname, port)
		default:
			err := attemptTLSConnection(hostname, port)
			if err == nil {
				log.Debug().Msgf("Successfully connected to the target environment %s:%d", hostname, port)
				return nil
			}
			log.Debug().Msgf("Attempt failed, retrying: %v", err)
			time.Sleep(1 * time.Second) // Wait before retrying
		}
	}
}

// attemptTLSConnection performs a TLS handshake and waits for initial data.
func attemptTLSConnection(hostname string, port int) error {
	address := fmt.Sprintf("%s:%d", hostname, port)
	conn, err := tls.Dial("tcp", address, &tls.Config{
		ServerName: hostname,
	})
	if err != nil {
		return fmt.Errorf("TLS connection failed: %v", err)
	}
	defer conn.Close()

	log.Debug().Msgf("TLS handshake completed, waiting to receive data from the server...")
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		return fmt.Errorf("error reading data from server: %v", err)
	}

	hexBytes := make([]string, n)
	for i := 0; i < n; i++ {
		hexBytes[i] = fmt.Sprintf("%02x", buffer[i])
	}

	log.Debug().Msgf("Received %d bytes from server: %s", n, hexBytes)
	return nil
}

// waitForHTTPServerToRespond pings a target URL until it returns a success status code or a timeout occurs.
func waitForHTTPServerToRespond(ctx context.Context, url string, timeout time.Duration) error {
	client := &http.Client{
		Timeout: 5 * time.Second, // Per-request timeout
	}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout reached while waiting for %s to respond", url)
		default:
			resp, err := client.Get(url)
			if err != nil {
				log.Info().Msgf("Error connecting to %s: %v. Retrying...", url, err)
			} else {
				defer resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					log.Debug().Msgf("Successfully connected to %s. Status: %s", url, resp.Status)
					return nil
				}
				log.Debug().Msgf("Received status code %d from %s. Retrying...", resp.StatusCode, url)
			}

			time.Sleep(2 * time.Second) // Wait before retrying
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
	taskRunner.AddTask("Wait for game server pods to be ready", func() error {
		return targetEnv.waitForGameServerReady(ctx, 3*time.Minute)
	})

	// CHECK CLIENT-FACING NETWORKING

	serverPrimaryAddress := envDetails.Deployment.ServerHostname
	serverPrimaryPort := 9339 // \todo should use envDetails.Deployment.ServerPorts but its occasionally empty
	log.Debug().Msgf("envDetails.Deployment.ServerPorts: %+v", envDetails.Deployment.ServerPorts)

	// Wait for the primary domain name to resolve to an IP address.
	taskRunner.AddTask("Wait for game server domain name to propagate", func() error {
		return waitForDomainResolution(serverPrimaryAddress, 15*time.Minute)
	})

	// Wait for server to respond to client traffic.
	taskRunner.AddTask("Wait for game server to serve clients", func() error {
		return waitForGameServerClientEndpointToBeReady(ctx, serverPrimaryAddress, serverPrimaryPort, 5*time.Minute)
	})

	// CHECK ADMIN INTERFACE

	// Wait for the admin domain name to resolve to an IP address.
	taskRunner.AddTask("Wait for LiveOps Dashboard domain name to propagate", func() error {
		return waitForDomainResolution(envDetails.Deployment.AdminHostname, 15*time.Minute)
	})

	// Wait for admin API to successfully respond to an HTTP request.
	taskRunner.AddTask("Wait for LiveOps Dashboard to serve traffic", func() error {
		return waitForHTTPServerToRespond(ctx, "https://"+envDetails.Deployment.AdminHostname, 2*time.Minute)
	})

	// Success
	return nil
}
