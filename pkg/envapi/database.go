/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/metaplay/cli/pkg/metahttp"
)

// Provider identifiers reported by the database operations API.
const (
	DatabaseProviderAWSRDS = "aws-rds"
	DatabaseProviderNone   = "none"
)

// Snapshot types as reported by the database operations API.
const (
	DatabaseSnapshotTypeManual    = "manual"
	DatabaseSnapshotTypeAutomated = "automated"
	DatabaseSnapshotTypeBackup    = "backup"
)

// Operation types as reported by the database operations API.
const (
	DatabaseOperationTypeSnapshotCreate = "snapshot-create"
	DatabaseOperationTypeSnapshotDelete = "snapshot-delete"
	DatabaseOperationTypeRollback       = "rollback"
)

// Operation statuses as reported by the database operations API.
const (
	DatabaseOperationStatusPending    = "pending"
	DatabaseOperationStatusInProgress = "in-progress"
	DatabaseOperationStatusCompleted  = "completed"
	DatabaseOperationStatusFailed     = "failed"
)

// DatabaseCapabilitiesResponse is the response from the capabilities endpoint.
// For environments where database operations are not supported (no dedicated
// database, unknown provider, etc.) the Shards slice is empty with HTTP 200.
type DatabaseCapabilitiesResponse struct {
	Shards []DatabaseShardCapabilities `json:"shards"`
}

// DatabaseShardCapabilities describes what operations are supported on a single shard.
type DatabaseShardCapabilities struct {
	ShardIndex         int                     `json:"shardIndex"`
	ClusterID          string                  `json:"clusterId"`
	Provider           string                  `json:"provider"`
	SupportsSnapshots  bool                    `json:"supportsSnapshots"`
	SupportsRollback   bool                    `json:"supportsRollback"`
	MaxManualSnapshots int                     `json:"maxManualSnapshots"`
	RollbackWindow     *DatabaseRollbackWindow `json:"rollbackWindow,omitempty"`
}

// DatabaseRollbackWindow is the inclusive time range within which a point-in-time
// rollback target can be selected.
type DatabaseRollbackWindow struct {
	EarliestTime time.Time `json:"earliestTime"`
	LatestTime   time.Time `json:"latestTime"`
}

// DatabaseInfoResponse is the response from the database info endpoint.
type DatabaseInfoResponse struct {
	Shards []DatabaseShardInfo `json:"shards"`
}

// DatabaseShardInfo describes the runtime state of a single database shard.
type DatabaseShardInfo struct {
	ShardIndex    int    `json:"shardIndex"`
	ClusterID     string `json:"clusterId"`
	Provider      string `json:"provider"`
	Status        string `json:"status"`
	Engine        string `json:"engine"`
	EngineVersion string `json:"engineVersion"`
}

// DatabaseSnapshotsResponse is the response from the list snapshots endpoint.
type DatabaseSnapshotsResponse struct {
	Snapshots []DatabaseSnapshot `json:"snapshots"`
}

// DatabaseSnapshot represents a single database snapshot. Name and Description
// are only populated for manual snapshots created via this API.
type DatabaseSnapshot struct {
	Identifier       string    `json:"identifier"`
	ShardIndex       int       `json:"shardIndex"`
	ClusterID        string    `json:"clusterId"`
	Type             string    `json:"type"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"createdAt"`
	Engine           string    `json:"engine"`
	EngineVersion    string    `json:"engineVersion"`
	AllocatedStorage int64     `json:"allocatedStorageGB"`
	Name             string    `json:"name,omitempty"`
	Description      string    `json:"description,omitempty"`
	Provider         string    `json:"provider"`
}

// DatabaseOperationsResponse is the response from the list operations endpoint.
// Only in-progress operations are returned.
type DatabaseOperationsResponse struct {
	Operations []DatabaseOperation `json:"operations"`
}

// DatabaseOperation represents an async database operation (snapshot create,
// snapshot delete, or rollback). The OperationID is prefixed with the operation
// type (e.g. "snapshot-create:", "rollback:") and should be treated as opaque.
type DatabaseOperation struct {
	OperationID string         `json:"operationId"`
	ShardIndex  int            `json:"shardIndex"`
	Type        string         `json:"type"`
	Status      string         `json:"status"`
	Progress    *int           `json:"progress,omitempty"`
	Error       *string        `json:"error,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	CompletedAt *time.Time     `json:"completedAt,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// IsTerminal reports whether the operation is in a terminal (completed or failed) state.
func (op *DatabaseOperation) IsTerminal() bool {
	return op.Status == DatabaseOperationStatusCompleted || op.Status == DatabaseOperationStatusFailed
}

// CreateDatabaseSnapshotRequest is the request body for creating a manual snapshot.
type CreateDatabaseSnapshotRequest struct {
	ShardIndex  int    `json:"shardIndex"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// RollbackDatabaseRequest is the request body for initiating a point-in-time rollback.
type RollbackDatabaseRequest struct {
	ShardIndex int       `json:"shardIndex"`
	TargetTime time.Time `json:"targetTime"`
}

// ListDatabaseSnapshotsOptions are the query options supported by the list
// snapshots endpoint. Zero values mean "unset".
type ListDatabaseSnapshotsOptions struct {
	Type  string // Filter by snapshot type. Empty means all types.
	Limit int    // Max snapshots returned per shard. 0 means unlimited.
}

// GetDatabaseCapabilities returns the per-shard capabilities for the environment's
// database operations API. An environment without dedicated database support returns
// an empty Shards slice (no error).
func (target *TargetEnvironment) GetDatabaseCapabilities() (*DatabaseCapabilitiesResponse, error) {
	path := fmt.Sprintf("/v0/databases/%s/capabilities", target.HumanID)
	resp, err := metahttp.Get[DatabaseCapabilitiesResponse](target.StackApiClient, path)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetDatabaseInfo returns per-shard cluster metadata for the environment.
// Returns an HTTP 400 for environments without database operations support.
func (target *TargetEnvironment) GetDatabaseInfo() (*DatabaseInfoResponse, error) {
	path := fmt.Sprintf("/v0/databases/%s/info", target.HumanID)
	resp, err := metahttp.Get[DatabaseInfoResponse](target.StackApiClient, path)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListDatabaseSnapshots returns snapshots across all shards, newest first.
// Supports optional type filter ("manual", "automated", "backup") and per-shard
// limit. Returns an empty list for unsupported environments (no error).
func (target *TargetEnvironment) ListDatabaseSnapshots(opts ListDatabaseSnapshotsOptions) (*DatabaseSnapshotsResponse, error) {
	path := fmt.Sprintf("/v0/databases/%s/snapshots", target.HumanID)
	query := url.Values{}
	if opts.Type != "" {
		query.Set("type", opts.Type)
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if encoded := query.Encode(); encoded != "" {
		path = path + "?" + encoded
	}
	resp, err := metahttp.Get[DatabaseSnapshotsResponse](target.StackApiClient, path)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetDatabaseSnapshot returns details for a single snapshot by identifier. The
// backend searches all shards so the caller does not need to know which shard
// owns the snapshot. Returns HTTP 404 if not found.
func (target *TargetEnvironment) GetDatabaseSnapshot(snapshotID string) (*DatabaseSnapshot, error) {
	path := fmt.Sprintf("/v0/databases/%s/snapshots/%s", target.HumanID, url.PathEscape(snapshotID))
	resp, err := metahttp.Get[DatabaseSnapshot](target.StackApiClient, path)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateDatabaseSnapshot initiates an async manual snapshot creation on the
// specified shard. Returns the initial operation with status "in-progress";
// poll GetDatabaseOperation to track completion.
func (target *TargetEnvironment) CreateDatabaseSnapshot(req *CreateDatabaseSnapshotRequest) (*DatabaseOperation, error) {
	path := fmt.Sprintf("/v0/databases/%s/snapshots", target.HumanID)
	resp, err := metahttp.PostJSON[DatabaseOperation](target.StackApiClient, path, req)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteDatabaseSnapshot initiates an async manual snapshot deletion. Only
// manual snapshots in "available" state can be deleted. Returns the initial
// operation; poll GetDatabaseOperation to track completion (the operation
// resolves to "completed" once the snapshot is no longer visible).
func (target *TargetEnvironment) DeleteDatabaseSnapshot(snapshotID string) (*DatabaseOperation, error) {
	path := fmt.Sprintf("/v0/databases/%s/snapshots/%s", target.HumanID, url.PathEscape(snapshotID))
	resp, err := metahttp.Delete[DatabaseOperation](target.StackApiClient, path, nil, "")
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// RollbackDatabase initiates an async point-in-time rollback (via Aurora
// Backtrack for AWS RDS). TargetTime must be within the shard's rollback
// window as reported by GetDatabaseCapabilities.
func (target *TargetEnvironment) RollbackDatabase(req *RollbackDatabaseRequest) (*DatabaseOperation, error) {
	path := fmt.Sprintf("/v0/databases/%s/rollback", target.HumanID)
	resp, err := metahttp.PostJSON[DatabaseOperation](target.StackApiClient, path, req)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListDatabaseOperations returns in-progress database operations across all shards.
// Completed operations are not listed — use GetDatabaseOperation to look up a
// specific operation by ID. Useful for discovering operations after CLI disconnect.
func (target *TargetEnvironment) ListDatabaseOperations() (*DatabaseOperationsResponse, error) {
	path := fmt.Sprintf("/v0/databases/%s/operations", target.HumanID)
	resp, err := metahttp.Get[DatabaseOperationsResponse](target.StackApiClient, path)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetDatabaseOperation polls the status of a single async operation by ID.
// Returns HTTP 404 if the operation does not exist.
func (target *TargetEnvironment) GetDatabaseOperation(operationID string) (*DatabaseOperation, error) {
	path := fmt.Sprintf("/v0/databases/%s/operations/%s", target.HumanID, url.PathEscape(operationID))
	resp, err := metahttp.Get[DatabaseOperation](target.StackApiClient, path)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
