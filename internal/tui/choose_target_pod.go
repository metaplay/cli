/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package tui

import (
	"fmt"

	humanize "github.com/dustin/go-humanize"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func GetPodDescription(pod *corev1.Pod) string {
	// Get readiness count
	readyContainers := 0
	totalContainers := len(pod.Spec.Containers)
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.Ready {
			readyContainers++
		}
	}

	// Format description with status, readiness, and age
	return fmt.Sprintf("[%s, %d/%d ready, %s]",
		pod.Status.Phase,
		readyContainers,
		totalContainers,
		humanize.Time(pod.CreationTimestamp.Time),
	)
}

func GetStatefulSetDescription(sts *appsv1.StatefulSet) string {
	// Format description with replicas and age, matching kubectl get sts output style
	return fmt.Sprintf("[%d/%d ready, %s]",
		sts.Status.ReadyReplicas,
		*sts.Spec.Replicas,
		humanize.Time(sts.CreationTimestamp.Time),
	)
}

func ChooseTargetPodDialog(pods []corev1.Pod) (*corev1.Pod, error) {
	if !isInteractiveMode {
		return nil, fmt.Errorf("interactive mode required for project selection")
	}

	// Let the user choose the target pod.
	selectedPod, err := ChooseFromListDialog(
		"Select Target Pod",
		pods,
		func(pod *corev1.Pod) (string, string) {
			return pod.Name, GetPodDescription(pod)
		},
	)
	if err != nil {
		return nil, err
	}

	log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), selectedPod.Name)

	return selectedPod, nil
}
