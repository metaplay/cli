/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"errors"
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

// idleContainerPidFile is where an idle debug container records the PID of its main process.
const idleContainerPidFile = "/tmp/.debug-container-pid"

// CreateIdleDebugContainer creates and starts a debug container in the target pod for commands to
// be exec'd into. The container stays up until the returned cleanup stops it, or at most lifetime,
// so that it does not stay up forever when the CLI does not get to run the cleanup. Everything
// exec'd into the container is killed when it ends, so lifetime must cover all of that work.
func CreateIdleDebugContainer(ctx context.Context, kubeCli *envapi.KubeClient, podName, targetContainerName string, lifetime time.Duration) (string, func(), error) {
	debugContainerName, err := CreateDebugContainer(ctx, kubeCli, podName, targetContainerName, false, false, idleContainerCommand(idleContainerPidFile, lifetime))
	if err != nil {
		return "", nil, err
	}

	// Stop the container by stopping its main process. A fresh context, so that the container is
	// stopped also after an interrupt has cancelled ctx.
	cleanup := func() {
		log.Debug().Msgf("Stop debug container %s", debugContainerName)
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _, err := ExecInDebugContainer(stopCtx, kubeCli, podName, debugContainerName, stopIdleContainerCommand(idleContainerPidFile))
		if err != nil {
			// Stopping the container can also end this command's stream with an error.
			log.Debug().Msgf("Stop debug container %s: %v", debugContainerName, err)
		}
	}
	return debugContainerName, cleanup, nil
}

// idleContainerCommand is the main process of an idle debug container: it records its PID in
// pidFile, so that stopIdleContainerCommand can stop it, and sleeps for lifetime. It sleeps also
// when the PID file cannot be written, for example on a read-only file system: then only the stop
// fails, and the container still ends after its lifetime. The process cannot be found by
// PID 1 or by name: the container shares its process namespace with its target container, so PID
// 1 is the target's main process, and other debug containers can run a 'sleep' too.
func idleContainerCommand(pidFile string, lifetime time.Duration) []string {
	return []string{"/bin/bash", "-c", fmt.Sprintf("echo $$ > %s; exec sleep %d", pidFile, int64(lifetime.Seconds()))}
}

// stopIdleContainerCommand returns the bash command that stops an idle debug container's main
// process, whose PID is in pidFile. That ends the container.
func stopIdleContainerCommand(pidFile string) string {
	return "kill $(cat " + pidFile + ")"
}

// CreateDebugContainer creates a debug container in the target pod that runs command, and waits
// for it to start. The container ends when command exits.
func CreateDebugContainer(ctx context.Context, kubeCli *envapi.KubeClient, podName, targetContainerName string, interactive bool, tty bool, command []string) (string, error) {
	// Create name for debug container.
	debugContainerName, err := createDebugContainerName()
	if err != nil {
		return "", err
	}
	log.Debug().Msgf("Create debug container %s: interactive=%v, tty=%v, command='%s'", debugContainerName, interactive, tty, strings.Join(command, " "))

	// Resolve target pod.
	pod, err := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get target pod %s: %w", podName, err)
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
		return "", fmt.Errorf("target container %s not found in pod %s", targetContainerName, podName)
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
		return "", err
	}

	// Wait for the debug container to be ready
	err = waitForContainerReady(ctx, kubeCli, podName, debugContainerName)
	if err != nil {
		return "", fmt.Errorf("debug container failed to become ready: %w", err)
	}

	return debugContainerName, nil
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

	err = StreamWithContext(ctx, exec, remotecommand.StreamOptions{
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

// StreamWithContext runs a remote command's stream. When ctx is done, the error wraps ctx's error:
// client-go flattens the cancellation into its error text, so without this, callers could not
// tell a cancelled command from a failed one with errors.Is.
func StreamWithContext(ctx context.Context, exec remotecommand.Executor, options remotecommand.StreamOptions) error {
	err := exec.StreamWithContext(ctx, options)
	if err != nil && ctx.Err() != nil && !errors.Is(err, ctx.Err()) {
		return &endedStreamError{ctxErr: ctx.Err(), streamErr: err}
	}
	return err
}

// endedStreamError is the error of a stream that failed after its context was done.
type endedStreamError struct {
	ctxErr    error
	streamErr error
}

// Error names the context's error once: client-go's text usually ends with it already.
func (e *endedStreamError) Error() string {
	if strings.Contains(e.streamErr.Error(), e.ctxErr.Error()) {
		return e.streamErr.Error()
	}
	return e.ctxErr.Error() + ": " + e.streamErr.Error()
}

func (e *endedStreamError) Unwrap() []error { return []error{e.ctxErr, e.streamErr} }

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
