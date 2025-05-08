/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// NewGameServerCR represents the structured CRD for the new operator GameServer.
type NewGameServerCR struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec struct {
		Config struct {
			EnvVars             []corev1.EnvVar `json:"envVars,omitempty"`
			RuntimeOptionsFiles []string        `json:"runtimeOptionsFiles,omitempty"`
			SecretRefs          []struct {
				Name    string `json:"name,omitempty"`
				KeyRefs []struct {
					Key string `json:"key,omitempty"`
				} `json:"keyRef,omitempty"`
			} `json:"secretRefs,omitempty"`
		} `json:"config,omitempty"`

		EnvironmentFamily string `json:"environmentFamily,omitempty"`

		Image struct {
			PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`
			Repository string            `json:"repository,omitempty"`
			Tag        string            `json:"tag,omitempty"`
		} `json:"image,omitempty"`

		Shards []struct {
			ClusterLabelSelector map[string]string `json:"clusterLabelSelector,omitempty"`
			MaxNodeCount         *int              `json:"maxNodeCount,omitempty"`
			MinNodeCount         *int              `json:"minNodeCount,omitempty"`
			Name                 string            `json:"name,omitempty"`
			PodTemplate          struct {
				ContainerTemplate struct {
					ExtraPorts []corev1.ContainerPort      `json:"extraPorts,omitempty"`
					Resources  corev1.ResourceRequirements `json:"resources,omitempty"`
				} `json:"containerTemplate,omitempty"`
			} `json:"podTemplate,omitempty"`
			Public     bool   `json:"public,omitempty"`
			Scaling    string `json:"scaling,omitempty"`
			Connection bool   `json:"connection,omitempty"`
			Admin      bool   `json:"admin,omitempty"`
			NodeCount  *int   `json:"nodeCount,omitempty"`
		} `json:"shards,omitempty"`
	} `json:"spec,omitempty"`

	Status struct {
		NodeSetConfigs []struct {
			GlobalSuffix string   `json:"globalSuffix,omitempty"`
			MaxNodeCount *int     `json:"maxNodeCount,omitempty"`
			MinNodeCount *int     `json:"minNodeCount,omitempty"`
			Name         string   `json:"name,omitempty"`
			Public       bool     `json:"public,omitempty"`
			Scaling      string   `json:"scaling,omitempty"`
			Connection   bool     `json:"connection,omitempty"`
			AdminAPI     bool     `json:"adminApi,omitempty"`
			EntityKinds  []string `json:"entityKinds,omitempty"`
			NodeCount    *int     `json:"nodeCount,omitempty"`
		} `json:"nodeSetConfigs,omitempty"`

		Phase  string `json:"phase,omitempty"`
		Shards map[string]struct {
			ClusterName  string `json:"clusterName,omitempty"`
			GlobalSuffix string `json:"globalSuffix,omitempty"`
		} `json:"shards,omitempty"`
	} `json:"status,omitempty"`
}

// Get a gameserver CR used by the new operator from the cluster.
func getGameServerNewCR(ctx context.Context, kubeCli *KubeClient) (*NewGameServerCR, error) {
	// GVR for new operator gameserver CR: gameservers.gameservers.metaplay.io
	var gvr = schema.GroupVersionResource{
		Group:    "gameservers.metaplay.io",
		Version:  "v0",
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
		return nil, fmt.Errorf("failed to marshal NewGameServerCR to JSON: %v", err)
	}

	// Parse JSON into structured Go struct
	var gameServerCR NewGameServerCR
	if err := json.Unmarshal(gameServerJSON, &gameServerCR); err != nil {
		return nil, fmt.Errorf("failed to unmarshal NewGameServerCR: %v", err)
	}

	// Output parsed NewGameServerCR struct
	log.Debug().Msgf("NewGameServerCR Name: %s", gameServerCR.Name)
	log.Debug().Msgf("Namespace: %s", gameServerCR.Namespace)
	log.Debug().Msgf("Spec: %+v", gameServerCR.Spec)
	log.Debug().Msgf("Status: %+v", gameServerCR.Status)

	return &gameServerCR, nil
}
