/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
)

// Helper function to create and start a standalone debug pod.
func CreateDebugPod(ctx context.Context, kubeCli *envapi.KubeClient, image string, interactive bool, tty bool, command []string) (string, func(), error) {
	// Create name for debug pod.
	debugPodName, err := createDebugContainerName()
	if err != nil {
		return "", nil, err
	}
	debugPodName = "debug-pod-" + debugPodName
	log.Debug().Msgf("Create debug pod %s: image=%s, interactive=%v, tty=%v, command='%s'", debugPodName, image, interactive, tty, strings.Join(command, " "))

	// Define the debug pod
	debugPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      debugPodName,
			Namespace: kubeCli.Namespace,
			Labels: map[string]string{
				"app":  "metaplay-debug",
				"type": "debug-pod",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:            "debug",
					Image:           image,
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
			},
		},
	}

	// Create the debug pod
	_, err = kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).Create(ctx, debugPod, metav1.CreateOptions{})
	if err != nil {
		log.Error().Msgf("Failed to create debug pod: %v", err)
		return "", nil, err
	}

	// Create cleanup function to delete the debug pod.
	cleanup := func() {
		log.Debug().Msgf("Deleting debug pod %s...", debugPodName)

		deletePolicy := metav1.DeletePropagationForeground
		err := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).Delete(ctx, debugPodName, metav1.DeleteOptions{
			PropagationPolicy: &deletePolicy,
		})
		if err != nil {
			log.Debug().Msgf("Failed to delete debug pod: %v", err)
		} else {
			log.Debug().Msgf("Successfully deleted debug pod %s", debugPodName)
		}
	}

	// Wait for the debug pod to be ready
	err = waitForPodReady(ctx, kubeCli, debugPodName)
	if err != nil {
		cleanup() // Clean up the pod if it failed to start
		return "", nil, err
	}

	return debugPodName, cleanup, nil
}

// waitForPodReady waits for the debug pod to be ready by watching for pod status changes.
func waitForPodReady(ctx context.Context, kubeCli *envapi.KubeClient, podName string) error {
	log.Debug().Msgf("Wait for debug pod to be ready: podName=%s", podName)

	// Create a field selector to filter events to only the specific pod we're interested in.
	fieldSelector := fields.OneTermEqualSelector("metadata.name", podName).String()

	// Create a ListWatch for the pod
	listWatch := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fieldSelector
			return kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = fieldSelector
			return kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).Watch(ctx, options)
		},
	}

	// Check if pod is already running
	preconditionFunc := func(store cache.Store) (bool, error) {
		obj, exists, err := store.GetByKey(kubeCli.Namespace + "/" + podName)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}

		pod, ok := obj.(*corev1.Pod)
		if !ok {
			return false, fmt.Errorf("expected Pod object, got %T", obj)
		}

		// Check if pod is running and all containers are ready
		if pod.Status.Phase == corev1.PodRunning {
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if !containerStatus.Ready {
					return false, nil
				}
			}
			return true, nil
		}

		// Check if pod failed
		if pod.Status.Phase == corev1.PodFailed {
			return false, fmt.Errorf("pod %s failed to start", podName)
		}

		return false, nil
	}

	// Set up a 60-second timeout context for the watch operation
	ctx, cancel := watchtools.ContextWithOptionalTimeout(ctx, time.Second*60)
	defer cancel()

	// Wait for the pod to be ready
	_, err := watchtools.UntilWithSync(ctx, listWatch, &corev1.Pod{}, preconditionFunc, func(event watch.Event) (bool, error) {
		pod, ok := event.Object.(*corev1.Pod)
		if !ok {
			return false, fmt.Errorf("expected Pod object, got %T", event.Object)
		}

		switch event.Type {
		case watch.Deleted:
			return false, fmt.Errorf("pod %s was deleted", podName)
		case watch.Modified, watch.Added:
			// Check if pod is running and all containers are ready
			if pod.Status.Phase == corev1.PodRunning {
				for _, containerStatus := range pod.Status.ContainerStatuses {
					if !containerStatus.Ready {
						return false, nil
					}
				}
				log.Debug().Msgf("Pod %s is ready", podName)
				return true, nil
			}

			// Check if pod failed
			if pod.Status.Phase == corev1.PodFailed {
				return false, fmt.Errorf("pod %s failed to start", podName)
			}
		}

		return false, nil
	})

	return err
}
