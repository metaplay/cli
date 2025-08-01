/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const metaplayGameServerPodLabelSelector = "app=metaplay-server"

type TargetShardSet struct {
	Name    string         // Name of the shardSet, also prefix for the pod names.
	Cluster *TargetCluster // Cluster on which the shard set resides on.
	// \todo other members: scaling, expectedPodCount, ..?
}

// Wrapper for a (single or multi-cluster) gameserver CR in an environment.
type TargetGameServer struct {
	Namespace       string           // Kubernetes namespace.
	GameServerNewCR *NewGameServerCR // GameServer CR for new operator.
	GameServerOldCR *OldGameServerCR // GameServer CR for old operator.
	KubeCli         *KubeClient      // Kubernetes clients for the primary cluster.
	Clusters        []TargetCluster  // All clusters associated with the environment (primary and edge clusters).
	ShardSets       []TargetShardSet // ShardSets belonging to the game server.
}

// Wrapper for accessing each cluster associated with the game server deployment.
type TargetCluster struct {
	KubeClient *KubeClient // Kubernetes client(s) to access target cluster.
	// \todo info about region, etc.
}

// Result of fetching all shardSets including their pods from all the clusters.
type ShardSetWithPods struct {
	ShardSet *TargetShardSet // ShardSet spec.
	Pods     []corev1.Pod    // Pods belonging to this shardSet.
}

// Find a gameserver pod with the given name.
func (gs *TargetGameServer) GetPod(podName string) (*KubeClient, *corev1.Pod, error) {
	// Resolve cluster based on podName
	ndx := strings.LastIndex(podName, "-")
	if ndx == -1 {
		return nil, nil, fmt.Errorf("invalid pod name: expecting game server pods to be of the form '<shard>-<ndx>', got '%s'", podName)
	}
	shardSetName := podName[:ndx]

	// Parse the pod index in the stateful set.
	// podNdxStr := podName[ndx+1:]
	// podNdx, err := strconv.Atoi(podNdxStr)
	// if err != nil {
	// 	return nil, nil, fmt.Errorf("invalid pod name: expecting game server pods to be of the form '<shard>-<ndx>', got '%s'", podName)
	// }

	// Get the shardSet with its pods.
	shardSet, err := gs.getShardSetByName(shardSetName)
	if err != nil {
		return nil, nil, err
	}

	// Get running game server pods in environment.
	kubeCli := shardSet.Cluster.KubeClient
	pod, err := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find gameserver pod named '%s': %w", podName, err)
	}

	return kubeCli, pod, nil
}

// Get the shardSet with given name. Can be on any cluster.
func (gs *TargetGameServer) GetShardSetWithPods(shardSetName string) (*ShardSetWithPods, error) {
	shardSet, err := gs.getShardSetByName(shardSetName)
	if err != nil {
		return nil, err
	}

	return gs.getShardSetPods(shardSet)
}

// Get all shardSets across all gameserver clusters and their asscoiated pods.
func (gs *TargetGameServer) GetAllShardSetsWithPods() ([]ShardSetWithPods, error) {
	var result []ShardSetWithPods

	// Iterate through all shardSets and get their pods
	for _, shardSet := range gs.ShardSets {
		// Get pods for this shardSet
		shardSetWithPods, err := gs.getShardSetPods(&shardSet)
		if err != nil {
			return nil, fmt.Errorf("failed to get pods for shardSet '%s': %w", shardSet.Name, err)
		}

		result = append(result, *shardSetWithPods)
	}

	return result, nil
}

func (gs *TargetGameServer) getShardSetByName(shardSetName string) (*TargetShardSet, error) {
	// Find the matching shardSet & return it with fetched pods.
	for _, shardSet := range gs.ShardSets {
		if shardSet.Name == shardSetName {
			return &shardSet, nil
		}
	}

	return nil, fmt.Errorf("shard set with name '%s' not found", shardSetName)
}

// Get all pods in the specified shardSet.
func (gs *TargetGameServer) getShardSetPods(shardSet *TargetShardSet) (*ShardSetWithPods, error) {
	kubeCli := shardSet.Cluster.KubeClient

	// Fetch the stateful set matching the shardSet.
	statefulSet, err := kubeCli.Clientset.AppsV1().StatefulSets(gs.Namespace).Get(context.TODO(), shardSet.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// Get running game server pods in environment.
	podList, err := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: metaplayGameServerPodLabelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list Kubernetes pods: %w", err)
	}

	// Filter pods that belong to the StatefulSet.
	filteredPods := []corev1.Pod{}
	for _, pod := range podList.Items {
		for _, ownerRef := range pod.OwnerReferences {
			if ownerRef.Kind == "StatefulSet" && ownerRef.UID == statefulSet.UID {
				filteredPods = append(filteredPods, pod)
				break
			}
		}
	}

	// Return the shardSet with pods.
	return &ShardSetWithPods{
		ShardSet: shardSet,
		Pods:     filteredPods,
	}, nil
}
