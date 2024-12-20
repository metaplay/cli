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

	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/core/v1"
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

// fetchGameServerPods retrieves pods with a specific label selector in a namespace.
func fetchGameServerPods(clientset *kubernetes.Clientset, namespace string) ([]v1.Pod, error) {
	log.Debug().Msgf("Fetching game server pods in namespace: %s", namespace)
	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: "app=metaplay-server",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pods: %w", err)
	}
	return pods.Items, nil
}

// resolvePodStatus determines the game server pod's phase and status message.
func resolvePodStatus(pod v1.Pod) GameServerPodStatus {
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

func findShardServerContainer(pod v1.Pod) *v1.ContainerStatus {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == "shard-server" {
			return &status
		}
	}
	return nil
}

// waitForPodsReady waits until all pods in a namespace are ready or a timeout occurs.
func waitForPodsReady(clientset *kubernetes.Clientset, namespace string, timeout time.Duration) error {
	startTime := time.Now()
	for time.Since(startTime) < timeout {
		pods, err := fetchGameServerPods(clientset, namespace)
		if err != nil {
			return err
		}

		allReady := true
		for _, pod := range pods {
			status := resolvePodStatus(pod)
			if status.Phase != PhaseReady {
				allReady = false
				log.Debug().Msgf("Pod %s not ready: %s", pod.Name, status.Message)
			}
		}

		if allReady {
			log.Debug().Msg("All pods are ready")
			return nil
		}

		time.Sleep(2 * time.Second)
	}
	return errors.New("timeout waiting for pods to be ready")
}

// fetchPodLogs fetches logs for a specific pod and container.
func fetchPodLogs(clientset *kubernetes.Clientset, namespace, podName, containerName string) (string, error) {
	log.Debug().Msgf("Fetching logs for pod %s, container %s", podName, containerName)
	logOptions := &v1.PodLogOptions{
		Container: containerName,
		Follow:    false,
		TailLines: int64Ptr(100),
	}

	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, logOptions)
	stream, err := req.Stream(context.TODO())
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
			log.Info().Msgf("Waiting for domain name %s to propagate... This can take up to 15 minutes on the first deploy.", hostname)
		} else {
			log.Debug().Msgf("Failed to resolve %s: %v. Retrying...", hostname, err)
		}

		// Delay before trying again -- these can take a while so avoid spamming the log
		time.Sleep(5 * time.Second)
	}
}

// waitForGameServerClientEndpointToBeReady waits until a game server client endpoint is ready by performing a TLS handshake.
func waitForGameServerClientEndpointToBeReady(hostname string, port int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

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
func waitForHTTPServerToRespond(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

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
				log.Info().Msgf("Received status code %d from %s. Retrying...", resp.StatusCode, url)
			}

			time.Sleep(2 * time.Second) // Wait before retrying
		}
	}
}

func (targetEnv *TargetEnvironment) WaitForServerToBeReady() error {
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

	// Wait for the pods to be ready
	log.Info().Msgf("Waiting for all Kubernetes pods to be ready...")
	err = waitForPodsReady(clientset, envDetails.Deployment.KubernetesNamespace, 15*time.Minute)
	if err != nil {
		return err
	}
	log.Info().Msgf("Game server pods are healthy and ready")

	// CHECK CLIENT-FACING NETWORKING

	// Wait for the primary domain name to resolve to an IP address.
	log.Info().Msgf("Checking that the game server is serving client traffic...")
	serverPrimaryAddress := envDetails.Deployment.ServerHostname
	serverPrimaryPort := 9339 // \todo make configurable?
	err = waitForDomainResolution(serverPrimaryAddress, 15*time.Minute)
	if err != nil {
		return err
	}

	// Wait for server to respond to client traffic.
	err = waitForGameServerClientEndpointToBeReady(serverPrimaryAddress, serverPrimaryPort, 5*time.Minute)
	if err != nil {
		return err
	}

	log.Info().Msgf("Game server is serving client traffic properly")

	// CHECK ADMIN INTERFACE

	log.Info().Msgf("Checking the game server LiveOps Dashboard...")

	// Wait for the admin domain name to resolve to an IP address.
	serverAdminAddress := strings.Replace(envDetails.Deployment.ServerHostname, ".", "-admin.", 1) // \todo huge hack
	err = waitForDomainResolution(serverAdminAddress, 15*time.Minute)
	if err != nil {
		return err
	}

	// Wait for admin API to successfully respond to an HTTP request.
	err = waitForHTTPServerToRespond("https://"+serverAdminAddress, 2*time.Minute)
	if err != nil {
		return err
	}

	log.Info().Msgf("Game server LiveOps Dashboard is ready")

	// Success
	return nil
}
