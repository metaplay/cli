/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package envapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// OldGameServerCR represents the structured CRD for the old operator GameServer.
type OldGameServerCR struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   metav1.ObjectMeta `json:"metadata"`
	Spec       struct {
		DedicatedShardNodes bool               `json:"dedicatedShardNodes"`
		ServiceSpec         corev1.ServiceSpec `json:"serviceSpec"`
		ShardSpec           []struct {
			Name        string                      `json:"name"`
			NodeCount   int                         `json:"nodeCount"`
			EntityKinds []string                    `json:"entityKinds"`
			Requests    corev1.ResourceRequirements `json:"requests"`
		} `json:"shardSpec"`
		StatefulSetSpec appsv1.StatefulSetSpec `json:"statefulSetSpec"`
	} `json:"spec"`
	Status struct {
		Phase             string   `json:"phase"`
		ShardConfigMap    string   `json:"shardConfigMap"`
		ShardServices     []string `json:"shardServices"`
		ShardStatefulSets []string `json:"shardStatefulSets"`
	} `json:"status"`
}

// Get a gameserver CR used by the old operator from the cluster.
func getGameServerOldCR(ctx context.Context, kubeCli *KubeClient) (*OldGameServerCR, error) {
	// GVR for the old operator gameserver CR: gameservers.metaplay.io
	gvr := schema.GroupVersionResource{
		Group:    "metaplay.io",
		Version:  "v1",
		Resource: "gameservers",
	}

	// Fetch all GameServers in the namespace
	gameServers, err := kubeCli.DynamicClient.Resource(gvr).Namespace(kubeCli.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch CR group=%s, version=%s, resource=%s from Kubernetes: %w", gvr.Group, gvr.Version, gvr.Resource, err)
	}
	if len(gameServers.Items) == 0 {
		// Not found, return nil (but not error)
		return nil, nil
	} else if len(gameServers.Items) > 1 {
		return nil, fmt.Errorf("multiple Kubernetes GameServer CRs found")
	}
	unstructuredObj := gameServers.Items[0]

	// Convert unstructured object to JSON
	gameServerJSON, err := unstructuredObj.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal OldGameServerCR to JSON: %v", err)
	}

	// Parse JSON into structured Go struct
	var gameServerCR OldGameServerCR
	if err := json.Unmarshal(gameServerJSON, &gameServerCR); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OldGameServerCR: %v", err)
	}

	// Output parsed OldGameServerCR struct
	log.Debug().Msgf("OldGameServerCR Name: %s", gameServerCR.Metadata.Name)
	log.Debug().Msgf("Namespace: %s", gameServerCR.Metadata.Namespace)
	log.Debug().Msgf("Shard ConfigMap: %s", gameServerCR.Status.ShardConfigMap)
	log.Debug().Msgf("First Container Image: %s", gameServerCR.Spec.StatefulSetSpec.Template.Spec.Containers[0].Image)

	return &gameServerCR, nil
}
