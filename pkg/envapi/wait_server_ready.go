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
	"sort"
	"strings"
	"time"

	"github.com/metaplay/cli/internal/tui"
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
	Details any                `json:"details,omitempty"`
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
	sort.Slice(ownedSets, func(i, j int) bool {
		return ownedSets[i].Name < ownedSets[j].Name
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

// shardPodStates holds the shard name and its pod states in order.
type shardPodStates struct {
	ShardName string
	Pods      []*corev1.Pod
}

// Fetch all game server pods from a namespace for the given shardSets.
// Return a slice of (shardName, []pods) in the same order as shardSets.
func fetchGameServerPodsByShardSet(ctx context.Context, kubeCli *KubeClient, shardSets []appsv1.StatefulSet) ([]shardPodStates, error) {
	// Fetch all gameserver pods in the namespace
	pods, err := FetchGameServerPods(ctx, kubeCli)
	if err != nil {
		return nil, err
	}

	// Prepare ordered result
	result := make([]shardPodStates, 0, len(shardSets))

	// Check that all expected pods from StatefulSets exist.
	for _, shardSet := range shardSets {
		numExpectedReplicas := int(*shardSet.Spec.Replicas)
		log.Debug().Msgf("StatefulSet '%s': expecting %d pod(s)", shardSet.Name, numExpectedReplicas)

		// Allocate a state for each expected pod in the stateful set.
		shardPods := make([]*corev1.Pod, numExpectedReplicas)

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

					// Store pod (found or not).
					foundPod = &pod
					break
				}
			}
			shardPods[shardNdx] = foundPod
		}

		result = append(result, shardPodStates{
			ShardName: shardSet.Name,
			Pods:      shardPods,
		})
	}

	return result, nil
}

// resolvePodStatus determines the game server pod's phase and status message.
func resolvePodStatus(pod corev1.Pod) GameServerPodStatus {
	containerStatus := findShardServerContainer(pod)
	if containerStatus == nil {
		// If there is no container created yet, try to resolve the cause
		if pod.Status.Phase == corev1.PodPending {
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
					if condition.Reason == "Unschedulable" {
						return GameServerPodStatus{
							Phase:   PhasePending,
							Message: "Pod is Unschedulable",
						}
					}
					return GameServerPodStatus{
						Phase:   PhasePending,
						Message: "Pod is not yet scheduled on any node",
					}
				}
			}
			return GameServerPodStatus{
				Phase:   PhasePending,
				Message: "Pod is Pending",
			}
		}
		if pod.Status.ContainerStatuses == nil || len(pod.Status.ContainerStatuses) == 0 {
			return GameServerPodStatus{
				Phase:   PhaseUnknown,
				Message: "ContainerStatuses is empty",
			}
		}
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
			Message: fmt.Sprintf("Container %s is running but not yet ready (app is starting)", containerStatus.Name),
			Details: state.Running,
		}

	case state.Waiting != nil:
		// Check for CrashLoopBackOff specifically
		if state.Waiting.Reason == "CrashLoopBackOff" {
			return GameServerPodStatus{
				Phase:   PhaseFailed,
				Message: fmt.Sprintf("Container %s is in CrashLoopBackOff: %s", containerStatus.Name, state.Waiting.Message),
				Details: state.Waiting,
			}
		}
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

// Check if the given gameserver CR (old or new) is ready.
// Only works with the old gameserver CRs (for now anyway).
// \todo Provide more detailed output as to what the status is -- to be used in various diagnostics
// \todo Consider using this with new operator as well: requires multi-region handling & proper CR<->sts ownership/revision relationships
func isGameServerReady(ctx context.Context, kubeCli *KubeClient, gameServer *TargetGameServer) (bool, []string, error) {
	// Must have either old or new operator CR.
	newCR := gameServer.GameServerNewCR
	oldCR := gameServer.GameServerOldCR
	if newCR == nil && oldCR == nil {
		log.Panic().Msg("Either old or new game server CR must be specified")
	} else if newCR != nil && oldCR != nil {
		log.Panic().Msg("Both new and old game server CRs cannot be specified")
	}

	// Fetch all game server StatefulSets owned by the game server.
	// \todo this only works in single-region setups .. use only with old operator?
	shardSets, err := fetchGameServerShardSets(ctx, kubeCli, newCR, oldCR)
	if err != nil {
		return false, nil, err
	}

	// If no matching StatefulSets, server is not ready.
	if len(shardSets) == 0 {
		return false, []string{"  No matching StatefulSets found"}, nil
	}

	// Fetch all the game server pods in the namespace.
	podsByShard, err := fetchGameServerPodsByShardSet(ctx, kubeCli, shardSets)
	if err != nil {
		return false, nil, err
	}

	// Check that all pods belonging to all shards are ready.
	allPodsReady := true
	statusLines := []string{}
	for _, shardPods := range podsByShard {
		// To update a deployment, metaplay-operator first scales StatefulSets to replicas=0, waits for shutdown and then recreates
		// the new setup. Hence, if StatefulSets.replicas = 0, we are still waiting for previous deployment to shut down.
		if len(shardPods.Pods) == 0 {
			statusLines = append(statusLines, fmt.Sprintf("  ShardSet '%s' shutting down previous deployment", shardPods.ShardName))
			allPodsReady = false
			continue
		}
		// Check that all expected pods are found.
		statusLines = append(statusLines, fmt.Sprintf("  ShardSet '%s' pods (%d):", shardPods.ShardName, len(shardPods.Pods)))
		for podNdx, pod := range shardPods.Pods {
			// Check that the pod is healthy & ready.
			podName := fmt.Sprintf("%s-%d", shardPods.ShardName, podNdx)
			if pod != nil {
				status := resolvePodStatus(*pod)
				statusLines = append(statusLines, fmt.Sprintf("    %s: %s [%s]", podName, status.Phase, status.Message))
				if status.Phase != PhaseReady {
					allPodsReady = false
				}

				// If pod failed, bail out with the logs from the pod
				if status.Phase == PhaseFailed {
					podLogs, err := fetchPodLogs(ctx, kubeCli, podName, "shard-server")
					if err != nil {
						log.Warn().Msgf("Failed to get logs from pod %s: %v", podName, err)
					} else {
						// Format logs with each line prefixed by '> '
						lines := strings.Split(podLogs, "\n")
						var sb strings.Builder
						for _, line := range lines {
							sb.WriteString(fmt.Sprintf("[%s] %s\n", podName, line))
						}
						log.Info().Msgf("Logs from pod %s:\n%s", podName, sb.String())
					}

					// Log info about failure & return the error
					log.Info().Msgf("Pod %s failed: %s", podName, status.Message)
					return false, nil, fmt.Errorf("pod %s failed to deploy (see above for logs and details)", podName)
				}
			} else {
				// Pod not in our filtered list - try to fetch it directly to see if it exists
				actualPod, err := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).Get(ctx, podName, metav1.GetOptions{})
				if err != nil {
					// Pod truly doesn't exist
					statusLines = append(statusLines, fmt.Sprintf("    %s: not found", podName))
				} else {
					// Pod exists but didn't pass our filters (old version, terminating, etc.)
					status := resolvePodStatus(*actualPod)
					statusLines = append(statusLines, fmt.Sprintf("    %s: %s [%s] (old version, being replaced)", podName, status.Phase, status.Message))
				}
				allPodsReady = false
			}
		}
	}

	// For the new game server, also check the CR status.
	isCRReady := true
	// \todo Check disabled for now due to operator not always setting CR phase reliably
	// if newCR != nil {
	// 	log.Debug().Msgf("New gameserver CR status.phase = %s", newCR.Status.Phase)
	// 	isCRReady = newCR.Status.Phase == "Running"
	// 	statusLines = append(statusLines, fmt.Sprintf("CR status: %s", newCR.Status.Phase))
	// }

	// Return whether everything is ready.
	isReady := isCRReady && allPodsReady
	return isReady, statusLines, nil
}

// waitForGameServerReady waits until the gameserver in a namespace is ready or a timeout occurs.
func (targetEnv *TargetEnvironment) waitForGameServerReady(ctx context.Context, output *tui.TaskOutput, timeout time.Duration) error {
	// Get target gameServer.
	gameServer, err := targetEnv.GetGameServer(ctx)
	if err != nil {
		return err
	}

	// Keep checking the gameservers until they are ready, or timeout is hit.
	startTime := time.Now()
	for time.Since(startTime) < timeout {
		// Get kube client for primary cluster.
		kubeCli, err := targetEnv.GetPrimaryKubeClient()
		if err != nil {
			return err
		}

		// Must have either old or new CR.
		if gameServer.GameServerNewCR == nil && gameServer.GameServerOldCR == nil {
			return fmt.Errorf("only new or old CR must be defined, not both")
		}

		// Get status of the deployment.
		// \todo handle edge clusters (for new CR only)
		isReady, statusLines, err := isGameServerReady(ctx, kubeCli, gameServer)
		if err != nil {
			return err
		}

		// Resolve status lines to show.
		crVersion := "new"
		if gameServer.GameServerOldCR != nil {
			crVersion = "old"
		}
		headerLines := append(
			[]string{fmt.Sprintf("Game server pod states (%s CR):", crVersion)},
			statusLines...,
		)

		// Show the game server shard/pod states.
		output.SetHeaderLines(headerLines)

		// If gamserver is ready, we're done.
		if isReady {
			return nil
		}

		// Wait a bit to check again (slower updates in non-interactive mode to avoid spamming the log).
		if tui.IsInteractiveMode() {
			time.Sleep(200 * time.Millisecond)
		} else {
			time.Sleep(2 * time.Second)
		}
	}
	return errors.New("timeout waiting for pods to be ready")
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
	defer stream.Close()

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

		if dnsErr, ok := err.(*net.DNSError); ok && dnsErr.IsNotFound {
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
			for iter := 0; iter < numAttempts; iter++ {
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
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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
				defer resp.Body.Close()
				switch {
				case resp.StatusCode >= 200 && resp.StatusCode < 300:
					// Accept 2xx (Success) status codes.
					output.AppendLinef("Successfully connected to %s. Status: %s", url, resp.Status)
					return nil
				case resp.StatusCode >= 300 && resp.StatusCode < 400:
					// Accept 3xx (Redirection) status codes.
					output.AppendLinef("Successfully received login redirect from %s. Status: %s", url, resp.Status)
					return nil
				}
				output.AppendLinef("Received status code %d from %s. Retrying...", resp.StatusCode, url)
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
