/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	scheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/remotecommand"
	watchtools "k8s.io/client-go/tools/watch"
)

// createDebugContainerName generates a unique debug container name with a random hex string.
func createDebugContainerName() (string, error) {
	// Generate a random 8-byte array
	randomBytes := make([]byte, 8)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Convert the bytes to a hex string
	hexString := hex.EncodeToString(randomBytes)

	// Create the debug container name
	return fmt.Sprintf("debugger-%s", hexString), nil
}

// Helper function to create and start a debug container in the target pod.
func createDebugContainer(ctx context.Context, kubeCli *envapi.KubeClient, podName, targetContainerName string, interactive bool, tty bool, command []string) (string, func(), error) {
	// Create name for debug container.
	debugContainerName, err := createDebugContainerName()
	if err != nil {
		return "", nil, err
	}
	log.Debug().Msgf("Create debug container %s: interactive=%v, tty=%v, command='%s'", debugContainerName, interactive, tty, strings.Join(command, " "))

	// Resolve target pod.
	pod, err := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("failed to get target pod %s: %v", podName, err)
	}

	// Verify target container exists
	targetContainerExists := false
	for _, container := range pod.Spec.Containers {
		if container.Name == targetContainerName {
			targetContainerExists = true
			break
		}
	}
	if !targetContainerExists {
		return "", nil, fmt.Errorf("target container %s not found in pod %s", targetContainerName, podName)
	}

	// Define the ephemeral container
	ephemeralContainer := &corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:            debugContainerName,
			Image:           "metaplay/diagnostics:latest",
			ImagePullPolicy: corev1.PullAlways,
			Stdin:           interactive,
			TTY:             tty,
			Command:         command,
			Env: []corev1.EnvVar{
				{
					Name:  "TERM",
					Value: "xterm-256color",
				},
			},
		},
		TargetContainerName: targetContainerName,
	}

	// Create ephemeral container using the ephemeral containers subresource
	pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, *ephemeralContainer)
	_, err = kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).UpdateEphemeralContainers(ctx, podName, pod, metav1.UpdateOptions{})
	if err != nil {
		log.Error().Msgf("Failed to start ephemeral debug container: %v", err)
		return "", nil, err
	}

	// Create cleanup function to terminate the ephemeral container.
	cleanup := func() {
		log.Debug().Msgf("Terminating debug container %s...", debugContainerName)

		// Try to terminate the container gracefully by sending exit command
		_, _, err := execInDebugContainer(ctx, kubeCli, podName, debugContainerName, "exit")
		if err != nil {
			log.Debug().Msgf("Container may have already terminated: %v", err)
		} else {
			log.Debug().Msgf("Successfully terminated debug container %s", debugContainerName)
		}
	}

	// Wait for the debug container to be ready
	err = waitForContainerReady(ctx, kubeCli, podName, debugContainerName)

	return debugContainerName, cleanup, nil
}

// waitForContainerReady waits for the debug container to be ready by watching for pod status changes.
// It uses the Kubernetes watch API to efficiently monitor container state transitions without polling.
//
// The function works in several steps:
// 1. Sets up a field selector to filter events for only our specific pod
// 2. Creates a ListWatch that handles both initial state and subsequent updates
// 3. Uses preconditionFunc to check if the container is already running in the initial state
// 4. Uses UntilWithSync to continuously watch for container state changes until it's running
//
// The watching process will continue until either:
// - The container enters the Running state (success)
// - The container terminates unexpectedly (error)
// - The context timeout is reached (error)
// - The pod is deleted (error)
func waitForContainerReady(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string) error {
	log.Debug().Msgf("Wait for debug container to be ready: podName=%s, debugContainerName=%s", podName, debugContainerName)

	// Create a field selector to filter events to only the specific pod we're interested in.
	// This is a Kubernetes API feature that allows server-side filtering of watch events,
	// reducing network traffic and processing overhead.
	fieldSelector := fields.OneTermEqualSelector("metadata.name", podName).String()
	// Create a ListWatch that combines both list and watch operations.
	// ListFunc gets the initial state, and WatchFunc streams subsequent changes.
	// Both use the field selector to filter for our specific pod.
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fieldSelector
			return kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = fieldSelector
			return kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).Watch(ctx, options)
		},
	}

	// Set up a 60-second timeout context for the watch operation
	// This prevents indefinite waiting if the container never reaches the desired state
	ctx, cancel := watchtools.ContextWithOptionalTimeout(ctx, time.Second*60)
	defer cancel()

	// preconditionFunc checks the initial state of the pod before starting the watch.
	// It verifies if the container is already running, which could happen if we
	// reconnect to an existing debug session.
	preconditionFunc := func(store cache.Store) (bool, error) {
		obj, exists, err := store.GetByKey(fmt.Sprintf("%s/%s", kubeCli.Namespace, podName))
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
		pod := obj.(*corev1.Pod)
		for _, status := range pod.Status.EphemeralContainerStatuses {
			if status.Name == debugContainerName && status.State.Running != nil {
				return true, nil
			}
		}
		return false, nil
	}

	// UntilWithSync efficiently combines initial state check and watch operations:
	// 1. First calls preconditionFunc to check if the condition is already met in the initial state
	// 2. If not met, starts watching for changes and calls the event handler for each change
	// 3. The event handler returns (true, nil) when the condition is met, ending the watch
	// 4. Returns error if the pod is deleted, container terminates, or timeout occurs
	_, err := watchtools.UntilWithSync(ctx, lw, &corev1.Pod{}, preconditionFunc, func(event watch.Event) (bool, error) {
		switch event.Type {
		case watch.Deleted:
			return false, fmt.Errorf("pod %s/%s was deleted", kubeCli.Namespace, podName)
		case watch.Added, watch.Modified:
			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				log.Debug().Msg("Watch: Received non-pod object")
				return false, nil
			}

			for _, status := range pod.Status.EphemeralContainerStatuses {
				if status.Name == debugContainerName {
					stateStr := "unknown"
					if status.State.Running != nil {
						stateStr = "running"
					} else if status.State.Terminated != nil {
						stateStr = fmt.Sprintf("terminated (exit code: %d)", status.State.Terminated.ExitCode)
					} else if status.State.Waiting != nil {
						stateStr = fmt.Sprintf("waiting (%s: %s)", status.State.Waiting.Reason, status.State.Waiting.Message)
					}
					log.Debug().Msgf("Watch: ephemeral container status: name=%s, state=%s", status.Name, stateStr)

					if status.State.Running != nil {
						log.Debug().Msgf("Container %s is now running", debugContainerName)
						return true, nil
					}
					if status.State.Terminated != nil {
						return false, fmt.Errorf("ephemeral container %s terminated with exit code %d: %s",
							debugContainerName,
							status.State.Terminated.ExitCode,
							status.State.Terminated.Message)
					}
					if status.State.Waiting != nil && status.State.Waiting.Message != "" {
						log.Debug().Msgf("Container %s waiting: %s", debugContainerName, status.State.Waiting.Message)
					}
				}
			}
			return false, nil
		}
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("error waiting for container: %v", err)
	}

	return nil
}

// execInDebugContainer executes a command in the debug container using Kubernetes API (replaces execCommand)
func execInDebugContainer(ctx context.Context, kubeCli *envapi.KubeClient, podName string, debugContainerName string, command string) (string, string, error) {
	req := kubeCli.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(kubeCli.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command:   []string{"/bin/bash", "-c", command},
			Container: debugContainerName,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(kubeCli.RestConfig, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("failed to create executor: %w", err)
	}

	stdOut := new(strings.Builder)
	stdErr := new(strings.Builder)

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdOut,
		Stderr: stdErr,
		Tty:    false,
	})

	if err != nil {
		return stdOut.String(), stdErr.String(), fmt.Errorf("error streaming command: %w, stdout: %s, stderr: %s", err, stdOut.String(), stdErr.String())
	}

	return stdOut.String(), stdErr.String(), nil
}

// progressTracker wraps an io.Writer and reports progress
type progressTracker struct {
	w                 io.Writer
	numProcessed      int64
	totalSize         int64
	lastProgress      int
	lastUpdateTime    time.Time
	minUpdateInterval time.Duration
}

func (pt *progressTracker) Write(p []byte) (int, error) {
	n, err := pt.w.Write(p)
	if err != nil {
		return n, err
	}
	pt.numProcessed += int64(n)

	// Calculate current progress percentage
	percentComplete := int(float64(pt.numProcessed) / float64(pt.totalSize) * 100)

	// Ensure we don't exceed 100%
	if percentComplete > 100 {
		percentComplete = 100
	}

	// Update progress if enough time has passed and we have new progress to report.
	// The final 100% is always reported.
	isComplete := pt.numProcessed >= pt.totalSize
	intervalElapsed := time.Since(pt.lastUpdateTime) >= pt.minUpdateInterval
	if isComplete || (intervalElapsed && percentComplete > pt.lastProgress) {
		pt.lastUpdateTime = time.Now()
		pt.lastProgress = percentComplete
		log.Info().Msgf("Copying: %d%%", percentComplete)
	}

	return n, nil
}

// copyFileFromPod copies a file from a pod using a tar pipe, similar to how kubectl cp works internally
// This version uses the Kubernetes API.
func copyFileFromPod(ctx context.Context, kubeCli *envapi.KubeClient, podName, containerName, srcDir, fileName, destPath string) error {
	// Create the destination directory if it doesn't exist
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %v", err)
	}

	// Command to create compressed tar stream in the container
	tarCmd := fmt.Sprintf("tar czf - -C %s %s", srcDir, fileName)

	// Create destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %v", err)
	}
	defer destFile.Close()

	reader, outStream := io.Pipe()

	// Construct the exec request
	req := kubeCli.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(kubeCli.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command:   []string{"sh", "-c", tarCmd},
			Container: containerName,
			Stdin:     false, // We're only reading the output
			Stdout:    true,
			Stderr:    true, // Capture stderr as well
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(kubeCli.RestConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	go func() {
		defer outStream.Close()
		err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: outStream,
			Stderr: os.Stderr, // Redirect Stderr to the current process's stderr
			Tty:    false,
		})
		if err != nil {
			log.Error().Msgf("Error streaming from pod: %v", err) // Log the error but continue
		}
	}()

	// Create gzip reader for decompression
	gzReader, err := gzip.NewReader(reader)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	// Extract tar stream
	tr := tar.NewReader(gzReader)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %v", err)
		}

		if hdr.Name != filepath.Base(fileName) {
			continue
		}

		// Create progress tracking writer
		fileSize := hdr.Size
		log.Info().Msgf("Heap dump file size: %s", humanizeFileSize(fileSize))

		progressWriter := io.Writer(destFile)
		if fileSize > 0 {
			progressWriter = &progressTracker{
				w:                 destFile,
				totalSize:         fileSize,
				minUpdateInterval: time.Second / 5, // Update at most X times per second
				lastUpdateTime:    time.Now(),
			}
		}

		if _, err := io.Copy(progressWriter, tr); err != nil {
			return fmt.Errorf("failed to copy file contents: %v", err)
		}
		break
	}

	return nil
}

// Humanize a file size to more readable format.
func humanizeFileSize(fileSize int64) string {
	const (
		KB = 1 << 10 // 1024 bytes
		MB = 1 << 20 // 1024 KB
		GB = 1 << 30 // 1024 MB
		TB = 1 << 40 // 1024 GB
	)

	switch {
	case fileSize >= TB:
		return fmt.Sprintf("%.2f TB", float64(fileSize)/TB)
	case fileSize >= GB:
		return fmt.Sprintf("%.2f GB", float64(fileSize)/GB)
	case fileSize >= MB:
		return fmt.Sprintf("%.2f MB", float64(fileSize)/MB)
	case fileSize >= KB:
		return fmt.Sprintf("%.2f KB", float64(fileSize)/KB)
	default:
		return fmt.Sprintf("%d B", fileSize)
	}
}

// Information about the running server process.
type serverProcessInfo struct {
	pid      int     // Pid of the server process.
	username string  // Username running the server process.
	memoryGB float64 // Heap size of server process in gigabytes.
}

// Helper function to get process information (PID, username, and memory usage) from
// a game server running in a pod.
func getServerProcessInformation(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string) (*serverProcessInfo, error) {
	// Get PID using kubectl exec in the debug container
	log.Debug().Msgf("Get game server process information...")
	stdout, _, err := execInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		`pgrep -f Server`,
	)
	if err != nil {
		log.Error().Msgf("Failed to get game server process information: %v", err) // Removed stderr from log, it's already piped
		return nil, err
	}

	// Parse and validate PID from stdout
	pidStr := strings.TrimSpace(stdout)
	if pidStr == "" {
		return nil, fmt.Errorf("unable to resolve game server PID: got empty response")
	}

	// Parse the PID.
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return nil, fmt.Errorf("invalid PID format '%s', must be a positive integer", pidStr)
	}
	log.Debug().Msgf("Found game server process with PID: %s", pidStr)

	// Get username running the Server process.
	log.Debug().Msgf("Get user which is running the game server process...")
	stdout, _, err = execInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		fmt.Sprintf("ps -o user= -p %s", pidStr),
	)
	if err != nil {
		log.Error().Msgf("Failed to get game server process information: %v", err) // Removed stderr from log, it's already piped
		return nil, err
	}

	// Parse and validate username from stdout.
	username := strings.TrimSpace(stdout)
	if username == "" {
		return nil, fmt.Errorf("unable to resolve user: got empty response")
	}

	// Validate that the username follows Unix/Linux username conventions
	if err := validateUnixUsername(username); err != nil {
		return nil, fmt.Errorf("invalid username '%s': %v", username, err)
	}
	log.Debug().Msgf("Game server is running as user: %s", username)

	// Get process memory information using standard Linux tools
	log.Debug().Msgf("Getting process memory information...")
	stdout, _, err = execInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		fmt.Sprintf("cat /proc/%s/status", pidStr),
	)
	if err != nil {
		log.Error().Msgf("Failed to get game server memory information: %v", err) // Removed stderr
		return nil, err
	}

	// log.Debug().Msgf("Full /proc/%s/status:\n%s", pid, stdout)

	// Parse memory information from /proc/[pid]/status
	// Format example:
	// VmSize:   12345678 kB
	// VmRSS:     1234567 kB
	var vmRss int64
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] != "kB" {
			continue
		}

		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}

		if fields[0] == "VmRSS:" {
			vmRss = value
		}
	}

	if vmRss == 0 {
		return nil, fmt.Errorf("failed to parse memory information from /proc/status")
	}

	// Convert from kB to GB
	memoryGB := float64(vmRss) / (1024 * 1024.0)
	return &serverProcessInfo{
		pid:      pid,
		username: username,
		memoryGB: memoryGB,
	}, nil
}
