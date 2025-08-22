/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Configuration for a single database shard.
type DatabaseShardConfig struct {
	ShardIndex    int    // added via code, not in the JSON
	DatabaseName  string `json:"DatabaseName"`
	Password      string `json:"Password"`
	ReadOnlyHost  string `json:"ReadOnlyHost"`
	ReadWriteHost string `json:"ReadWriteHost"`
	UserId        string `json:"UserId"`
}

// Configuration for the environment's database and shards.
type MetaplayInfraDatabase struct {
	Backend         string                `json:"Backend"`
	NumActiveShards int                   `json:"NumActiveShards"`
	Shards          []DatabaseShardConfig `json:"Shards"`
}

// Contents of the 'options.json' field in the 'metaplay-deployment-runtime-options' Kubernetes secret.
type MetaplayInfraOptions struct {
	Database MetaplayInfraDatabase `json:"Database"`
}

// FetchDatabaseShardsFromSecret fetches database shard configuration from the 'metaplay-deployment-runtime-options' Kubernetes secret.
func FetchDatabaseShardsFromSecret(ctx context.Context, kubeCli *envapi.KubeClient, namespace string) ([]DatabaseShardConfig, error) {
	// Get the metaplay-deployment-runtime-options secret.
	log.Debug().Msg("Fetching Kubernetes secret 'metaplay-deployment-runtime-options'...")
	secret, err := kubeCli.Clientset.CoreV1().Secrets(namespace).Get(ctx, "metaplay-deployment-runtime-options", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes secret 'metaplay-deployment-runtime-options': %w", err)
	}

	// Get the options.json data from the secret.
	optionsJSON, exists := secret.Data["options.json"]
	if !exists {
		return nil, fmt.Errorf("options.json not found in secret")
	}
	log.Debug().Msgf("Found infrastructure options.json in secret: %s", string(optionsJSON))

	// Parse contents of options.json.
	var infraOptions MetaplayInfraOptions
	if err := json.Unmarshal(optionsJSON, &infraOptions); err != nil {
		return nil, fmt.Errorf("failed to parse runtime options JSON: %w", err)
	}
	log.Debug().Msgf("Parsed infrastructure options.json: %+v", infraOptions)

	// Must have at least one shard.
	if len(infraOptions.Database.Shards) == 0 {
		return nil, fmt.Errorf("no database shards found in infra configuration")
	}

	log.Debug().Msgf("Found %d database shard(s) in infra options.json", len(infraOptions.Database.Shards))
	return infraOptions.Database.Shards, nil
}
