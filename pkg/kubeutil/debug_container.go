/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/remotecommand"
	watchtools "k8s.io/client-go/tools/watch"
)

// Helper function to create and start a debug container in the target pod.
func CreateDebugContainer(ctx context.Context, kubeCli *envapi.KubeClient, podName, targetContainerName string, interactive bool, tty bool, command []string) (string, func(), error) {
	// Create name for debug container.
	debugContainerName, err := createDebugContainerName()
	if err != nil {
		return "", nil, err
	}
	log.Debug().Msgf("Create debug container %s: interactive=%v, tty=%v, command='%s'", debugContainerName, interactive, tty, strings.Join(command, " "))

	// Resolve target pod.
	pod, err := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("failed to get target pod %s: %w", podName, err)
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
			SecurityContext: &corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{
					// Enable ptrace to allow debugging/tracing. Should be equivalent to 'kubectl debug --profile=general'.
					Add: []corev1.Capability{"SYS_PTRACE"},
				},
			},
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
		_, _, err := ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName, "exit")
		if err != nil {
			log.Debug().Msgf("Container may have already terminated: %v", err)
		} else {
			log.Debug().Msgf("Successfully terminated debug container %s", debugContainerName)
		}
	}

	// Wait for the debug container to be ready
	err = waitForContainerReady(ctx, kubeCli, podName, debugContainerName)
	if err != nil {
		cleanup() // Clean up the container we created
		return "", nil, fmt.Errorf("debug container failed to become ready: %w", err)
	}

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
	// The list call gets the initial state, and the watch call streams subsequent changes.
	// Both use the field selector to filter for our specific pod.
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fieldSelector
			return kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
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
		return fmt.Errorf("error waiting for container: %w", err)
	}

	return nil
}

// ExecInDebugContainer executes a command in the debug container using Kubernetes API (replaces execCommand)
func ExecInDebugContainer(ctx context.Context, kubeCli *envapi.KubeClient, podName string, debugContainerName string, command string) (string, string, error) {
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

	err = streamWithContext(ctx, exec, remotecommand.StreamOptions{
		Stdout: stdOut,
		Stderr: stdErr,
		Tty:    false,
	})

	if err != nil {
		return stdOut.String(), stdErr.String(), fmt.Errorf("error streaming command: %w, stdout: %s, stderr: %s", err, stdOut.String(), stdErr.String())
	}

	return stdOut.String(), stdErr.String(), nil
}

// RemoveFilesInDebugContainer removes files in the debug container with a single exec. Files that
// do not exist are not an error.
func RemoveFilesInDebugContainer(ctx context.Context, kubeCli *envapi.KubeClient, podName string, debugContainerName string, paths ...string) error {
	command := removeFilesCommand(paths)
	log.Debug().Msgf("Remove files from debug container: %s", command)
	_, _, err := ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName, command)
	return err
}

// removeFilesCommand returns the bash command that removes the files. It first stops a download of
// the file that was cancelled: its shell, 'dd' and 'gzip' (see streamFileFromOffset). Cancelling
// the exec stream does not stop them, and 'dd' would hold the removed file's disk space until the
// container ends.
func removeFilesCommand(paths []string) string {
	var command strings.Builder
	quoted := make([]string, len(paths))
	for i, path := range paths {
		quoted[i] = shellQuote(path)
		// The pattern matches the download's shell and its 'dd'. Its [d] keeps it from matching
		// this command's own shell. Killing the shell's children stops the 'gzip'.
		pattern := shellQuote("[d]d if=" + regexp.QuoteMeta(path) + " ")
		command.WriteString("for pid in $(pgrep -f -- " + pattern + "); do pkill -P $pid; kill $pid; done; ")
	}
	return command.String() + "rm -f -- " + strings.Join(quoted, " ")
}

// shellQuote quotes s as a single word for bash, so a path with spaces or wildcards in it, such as
// one built from a user's --output, means only itself.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// streamWithContext runs a remote command's stream. When ctx is done, the error wraps ctx's error:
// client-go flattens the cancellation into its error text, so without this, callers could not
// tell a cancelled command from a failed one with errors.Is.
func streamWithContext(ctx context.Context, exec remotecommand.Executor, options remotecommand.StreamOptions) error {
	err := exec.StreamWithContext(ctx, options)
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("%w: %w", ctx.Err(), err)
	}
	return err
}

// ExecInDebugContainerUntilDone is ExecInDebugContainer for a command that must not be abandoned
// when ctx is cancelled: cancelling the exec stream does not stop the process in the pod, which
// would go on unobserved. The command is given up only grace after ctx is cancelled, so an
// interrupted CLI command does not wait for it forever. The caller must not call this with a ctx
// that is already cancelled: the task runner does not start a task then.
func ExecInDebugContainerUntilDone(ctx context.Context, grace time.Duration, kubeCli *envapi.KubeClient, podName, debugContainerName, command string) (string, string, error) {
	continued, cancel := continueAfterCancel(ctx, grace)
	defer cancel()
	return ExecInDebugContainer(continued, kubeCli, podName, debugContainerName, command)
}

// continueAfterCancel returns a context that is not cancelled with ctx, but grace after ctx is.
func continueAfterCancel(ctx context.Context, grace time.Duration) (context.Context, context.CancelFunc) {
	continued, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stopWaiting := context.AfterFunc(ctx, func() {
		select {
		case <-time.After(grace):
			cancel()
		case <-continued.Done(): // cancel was called early
		}
	})
	return continued, func() {
		stopWaiting()
		cancel()
	}
}
