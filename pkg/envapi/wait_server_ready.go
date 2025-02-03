/*
 * Copyright Metaplay. All rights reserved.
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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
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

type GameServerPodStatus struct {
	Phase   GameServerPodPhase `json:"phase"`
	Message string             `json:"message"`
	Details interface{}        `json:"details,omitempty"`
}

// NewKubernetesClientSet creates a new Kubernetes clientset to operate against the environment.
func (targetEnv *TargetEnvironment) NewKubernetesClientSet() (*kubernetes.Clientset, error) {
	// Get kubeconfig to access the environment.
	log.Debug().Msg("Fetch kubeconfig with embedded credentials")
	kubeconfigPayload, err := targetEnv.GetKubeConfigWithEmbeddedCredentials()
	if err != nil {
		return nil, err
	}

	// Use the kubeconfig payload as a reader
	log.Debug().Msg("Create Kubernetes clientConfig")
	clientConfig, err := clientcmd.NewClientConfigFromBytes([]byte(*kubeconfigPayload))
	if err != nil {
		return nil, err
	}

	// Convert clientConfig to *rest.Config
	log.Debug().Msg("Create Kubernetes restConfig")
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	// Create the Kubernetes clientset
	log.Debug().Msg("Create Kubernetes clientset")
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

func fetchGameServerShardSets(ctx context.Context, clientset *kubernetes.Clientset, namespace string) ([]appsv1.StatefulSet, error) {
	log.Debug().Msgf("Fetch game server stateful sets in namespace: %s", namespace)
	statefulSets, err := clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=metaplay-server",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pods: %w", err)
	}
	return statefulSets.Items, nil
}

// FetchGameServerPods retrieves pods with a specific label selector in a namespace.
func FetchGameServerPods(ctx context.Context, clientset *kubernetes.Clientset, namespace string) ([]corev1.Pod, error) {
	log.Debug().Msgf("Fetch game server pods in namespace: %s", namespace)
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=metaplay-server",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pods: %w", err)
	}
	return pods.Items, nil
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

	log.Debug().Msgf("Pod %s container status: %+v", pod.Name, containerStatus)
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

// Check whether all game servers pods are ready and healthy.
func areGameServerPodsReady(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (bool, error) {
	// \todo Fetch the top-level resource first to identify with StatefulSets should be present

	// Fetch all game server StatefulSets.
	shardSets, err := fetchGameServerShardSets(ctx, clientset, namespace)
	if err != nil {
		return false, err
	}

	if len(shardSets) == 0 {
		return false, fmt.Errorf("no matching game server StatefulSets found in the environment")
	}

	// Fetch all the game server pods in the namespace.
	pods, err := FetchGameServerPods(ctx, clientset, namespace)
	if err != nil {
		return false, err
	}

	// Check that all expected pods from StatefulSets exist
	for _, shardSet := range shardSets {
		numExpectedReplicas := int(*shardSet.Spec.Replicas)
		log.Debug().Msgf("StatefulSet %s: %d pod(s)", shardSet.Name, numExpectedReplicas)

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

			// If pod not found, we're not ready
			if foundPod == nil {
				log.Debug().Msgf("Pod %s not found", podName)
				return false, nil
			}

			// Check that the pod is healthy & ready.
			status := resolvePodStatus(*foundPod)
			if status.Phase != PhaseReady {
				log.Debug().Msgf("Pod %s not ready: %s", podName, status.Message)
				return false, nil
			}
		}
	}

	// All pods in all shard sets are ready!
	log.Debug().Msg("All pods are ready")
	return true, nil
}

// waitForGameServerPodsReady waits until all pods in a namespace are ready or a timeout occurs.
func waitForGameServerPodsReady(ctx context.Context, clientset *kubernetes.Clientset, namespace string, timeout time.Duration) error {
	// Wait a bit to let StatefulSets propagate
	// \todo use a more robust way to check them
	time.Sleep(2)

	startTime := time.Now()
	for time.Since(startTime) < timeout {
		// Check whether all pods are ready.
		isReady, err := areGameServerPodsReady(ctx, clientset, namespace)
		if err != nil {
			return err
		}

		// If ready, we're done.
		if isReady {
			return nil
		}

		// Wait a bit to check again.
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("timeout waiting for pods to be ready")
}

// fetchPodLogs fetches logs for a specific pod and container.
func fetchPodLogs(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName, containerName string) (string, error) {
	log.Debug().Msgf("Fetching logs for pod %s, container %s", podName, containerName)
	logOptions := &corev1.PodLogOptions{
		Container: containerName,
		Follow:    false,
		TailLines: int64Ptr(100),
	}

	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, logOptions)
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
	// Initialize a Kubernetes clientset against the environment
	clientset, err := targetEnv.NewKubernetesClientSet()
	if err != nil {
		return err
	}

	// Fetch environment details.
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return err
	}

	// Wait for the pods to be ready.
	// Only wait for a few minutes as pods generally become healthy fairly
	// soon as we want to display the logs from errors early.
	taskRunner.AddTask("Wait for all Kubernetes pods to be ready", func() error {
		return waitForGameServerPodsReady(ctx, clientset, targetEnv.HumanId, 3*time.Minute)
	})

	// CHECK CLIENT-FACING NETWORKING

	serverPrimaryAddress := envDetails.Deployment.ServerHostname
	serverPrimaryPort := 9339 // \todo should have envDetails.Deployment.ServerPorts but it returns empty values
	// if envDetails.Deployment.ServerPorts == nil || len(envDetails.Deployment.ServerPorts) == 0 {
	// 	log.Warn().Msgf("envDetails.Deployment.ServerPorts: %+v", envDetails.Deployment.ServerPorts)
	// }

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
