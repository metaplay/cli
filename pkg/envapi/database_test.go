/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/metahttp"
)

// newTestTargetEnvironment returns a TargetEnvironment wired to the given test
// server base URL (used in place of the usual https://infra.<stack>/stackapi).
func newTestTargetEnvironment(baseURL string) *TargetEnvironment {
	tokenSet := &auth.TokenSet{AccessToken: "test-token"}
	return &TargetEnvironment{
		TokenSet:        tokenSet,
		StackApiBaseURL: baseURL,
		HumanID:         "test-env",
		StackApiClient:  metahttp.NewJSONClient(tokenSet, baseURL),
	}
}

// recordingHandler records the request received and returns the configured response.
type recordingHandler struct {
	method     string
	path       string
	rawQuery   string
	body       []byte
	statusCode int
	response   string
}

func (h *recordingHandler) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.method = r.Method
		h.path = r.URL.Path
		h.rawQuery = r.URL.RawQuery
		body, _ := io.ReadAll(r.Body)
		h.body = body
		w.Header().Set("Content-Type", "application/json")
		if h.statusCode == 0 {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(h.statusCode)
		}
		if h.response != "" {
			_, _ = w.Write([]byte(h.response))
		}
	})
}

func TestGetDatabaseCapabilities_Success(t *testing.T) {
	h := &recordingHandler{response: `{
        "shards": [
            {
                "shardIndex": 0,
                "clusterId": "mygame-prod-0",
                "provider": "aws-rds",
                "supportsSnapshots": true,
                "supportsRollback": true,
                "maxManualSnapshots": 5,
                "rollbackWindow": {
                    "earliestTime": "2026-03-08T00:00:00Z",
                    "latestTime": "2026-04-09T15:25:00Z"
                }
            }
        ]
    }`}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	target := newTestTargetEnvironment(server.URL)
	caps, err := target.GetDatabaseCapabilities()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.method != http.MethodGet {
		t.Errorf("expected GET, got %s", h.method)
	}
	if h.path != "/v0/databases/test-env/capabilities" {
		t.Errorf("unexpected path: %s", h.path)
	}
	if len(caps.Shards) != 1 {
		t.Fatalf("expected 1 shard, got %d", len(caps.Shards))
	}
	shard := caps.Shards[0]
	if shard.ClusterID != "mygame-prod-0" {
		t.Errorf("unexpected cluster id: %s", shard.ClusterID)
	}
	if !shard.SupportsRollback || !shard.SupportsSnapshots {
		t.Errorf("expected both capabilities true, got snap=%v rollback=%v", shard.SupportsSnapshots, shard.SupportsRollback)
	}
	if shard.MaxManualSnapshots != 5 {
		t.Errorf("unexpected quota: %d", shard.MaxManualSnapshots)
	}
	if shard.RollbackWindow == nil {
		t.Fatal("expected rollback window to be present")
	}
	if shard.RollbackWindow.EarliestTime.IsZero() || shard.RollbackWindow.LatestTime.IsZero() {
		t.Errorf("expected parsed times, got earliest=%v latest=%v", shard.RollbackWindow.EarliestTime, shard.RollbackWindow.LatestTime)
	}
}

func TestGetDatabaseCapabilities_UnsupportedReturnsEmpty(t *testing.T) {
	h := &recordingHandler{response: `{"shards": []}`}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	caps, err := newTestTargetEnvironment(server.URL).GetDatabaseCapabilities()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(caps.Shards) != 0 {
		t.Errorf("expected 0 shards, got %d", len(caps.Shards))
	}
}

func TestGetDatabaseInfo_Success(t *testing.T) {
	h := &recordingHandler{response: `{
        "shards": [
            {"shardIndex": 0, "clusterId": "mygame-prod-0", "provider": "aws-rds", "status": "available", "engine": "aurora-mysql", "engineVersion": "8.0.mysql_aurora.3.10.3"}
        ]
    }`}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	info, err := newTestTargetEnvironment(server.URL).GetDatabaseInfo()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.path != "/v0/databases/test-env/info" {
		t.Errorf("unexpected path: %s", h.path)
	}
	if len(info.Shards) != 1 || info.Shards[0].Engine != "aurora-mysql" {
		t.Errorf("unexpected response: %+v", info)
	}
}

func TestGetDatabaseInfo_UnsupportedReturnsHTTPError(t *testing.T) {
	h := &recordingHandler{
		statusCode: http.StatusBadRequest,
		response:   `{"error":"database operations not supported for this environment"}`,
	}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	_, err := newTestTargetEnvironment(server.URL).GetDatabaseInfo()
	var httpErr *metahttp.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *metahttp.HTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", httpErr.StatusCode)
	}
	if httpErr.Message != "database operations not supported for this environment" {
		t.Errorf("unexpected message: %q", httpErr.Message)
	}
}

func TestListDatabaseSnapshots_QueryParams(t *testing.T) {
	h := &recordingHandler{response: `{"snapshots":[]}`}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	target := newTestTargetEnvironment(server.URL)

	// No opts → no query string.
	if _, err := target.ListDatabaseSnapshots(ListDatabaseSnapshotsOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.rawQuery != "" {
		t.Errorf("expected empty query, got %q", h.rawQuery)
	}

	// Type filter only.
	if _, err := target.ListDatabaseSnapshots(ListDatabaseSnapshotsOptions{Type: "manual"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.rawQuery != "type=manual" {
		t.Errorf("unexpected query: %q", h.rawQuery)
	}

	// Type + limit.
	if _, err := target.ListDatabaseSnapshots(ListDatabaseSnapshotsOptions{Type: "automated", Limit: 10}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(h.rawQuery, "type=automated") || !strings.Contains(h.rawQuery, "limit=10") {
		t.Errorf("unexpected query: %q", h.rawQuery)
	}
}

func TestListDatabaseSnapshots_ParsesResults(t *testing.T) {
	h := &recordingHandler{response: `{
        "snapshots": [
            {
                "identifier": "mygame-prod-0-manual-20260409-153042",
                "shardIndex": 0,
                "clusterId": "mygame-prod-0",
                "type": "manual",
                "status": "available",
                "createdAt": "2026-04-09T15:30:42Z",
                "engine": "aurora-mysql",
                "engineVersion": "8.0.mysql_aurora.3.10.3",
                "allocatedStorageGB": 1,
                "name": "pre-migration",
                "description": "Before v2.3",
                "provider": "aws-rds"
            }
        ]
    }`}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	resp, err := newTestTargetEnvironment(server.URL).ListDatabaseSnapshots(ListDatabaseSnapshotsOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(resp.Snapshots))
	}
	s := resp.Snapshots[0]
	if s.Name != "pre-migration" || s.Type != "manual" || s.Provider != "aws-rds" {
		t.Errorf("unexpected snapshot: %+v", s)
	}
	if s.AllocatedStorage != 1 {
		t.Errorf("unexpected allocated storage: %d", s.AllocatedStorage)
	}
}

func TestGetDatabaseSnapshot_NotFound(t *testing.T) {
	h := &recordingHandler{
		statusCode: http.StatusNotFound,
		response:   `{"error":"snapshot not found: foo"}`,
	}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	_, err := newTestTargetEnvironment(server.URL).GetDatabaseSnapshot("foo")
	var httpErr *metahttp.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 HTTPError, got %v", err)
	}
	if h.path != "/v0/databases/test-env/snapshots/foo" {
		t.Errorf("unexpected path: %s", h.path)
	}
}

func TestGetDatabaseSnapshot_PathEscapesID(t *testing.T) {
	// Snapshot identifiers can contain ':' (e.g. automated: "rds:mygame-prod-0-..."),
	// which must be path-escaped to survive strict URL routers.
	h := &recordingHandler{response: `{
        "identifier": "rds:mygame-prod-0-2026-04-09",
        "shardIndex": 0,
        "clusterId": "mygame-prod-0",
        "type": "automated",
        "status": "available",
        "createdAt": "2026-04-09T15:30:42Z",
        "engine": "aurora-mysql",
        "engineVersion": "8.0.mysql_aurora.3.10.3",
        "allocatedStorageGB": 1,
        "provider": "aws-rds"
    }`}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	_, err := newTestTargetEnvironment(server.URL).GetDatabaseSnapshot("rds:mygame-prod-0-2026-04-09")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// url.PathEscape preserves ':' in paths (RFC 3986 allows it) but does not
	// add stray characters; we assert the raw path contains the full id.
	if !strings.HasSuffix(h.path, "rds:mygame-prod-0-2026-04-09") {
		t.Errorf("unexpected escaped path: %s", h.path)
	}
}

func TestCreateDatabaseSnapshot_SendsBody(t *testing.T) {
	h := &recordingHandler{
		statusCode: http.StatusCreated,
		response: `{
            "operationId": "snapshot-create:mygame-prod-0-manual-20260409-153042",
            "shardIndex": 0,
            "type": "snapshot-create",
            "status": "in-progress",
            "createdAt": "2026-04-09T15:30:42Z",
            "metadata": {"snapshotIdentifier": "mygame-prod-0-manual-20260409-153042"}
        }`,
	}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	req := &CreateDatabaseSnapshotRequest{
		ShardIndex:  0,
		Name:        "pre-migration",
		Description: "Before v2.3",
	}
	op, err := newTestTargetEnvironment(server.URL).CreateDatabaseSnapshot(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.method != http.MethodPost {
		t.Errorf("expected POST, got %s", h.method)
	}
	if h.path != "/v0/databases/test-env/snapshots" {
		t.Errorf("unexpected path: %s", h.path)
	}

	// Verify the request body was sent with the expected shape.
	var sent CreateDatabaseSnapshotRequest
	if err := json.Unmarshal(h.body, &sent); err != nil {
		t.Fatalf("failed to decode sent body: %v", err)
	}
	if sent != *req {
		t.Errorf("sent body %+v does not match request %+v", sent, *req)
	}

	if op.OperationID != "snapshot-create:mygame-prod-0-manual-20260409-153042" {
		t.Errorf("unexpected operation ID: %s", op.OperationID)
	}
	if op.Status != DatabaseOperationStatusInProgress {
		t.Errorf("unexpected status: %s", op.Status)
	}
	if op.IsTerminal() {
		t.Error("expected IsTerminal to be false for in-progress operation")
	}
}

func TestCreateDatabaseSnapshot_QuotaExceeded(t *testing.T) {
	h := &recordingHandler{
		statusCode: http.StatusConflict,
		response:   `{"error":"manual snapshot quota exceeded: 5/5 snapshots for this shard"}`,
	}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	_, err := newTestTargetEnvironment(server.URL).CreateDatabaseSnapshot(&CreateDatabaseSnapshotRequest{ShardIndex: 0})
	var httpErr *metahttp.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", httpErr.StatusCode)
	}
	if !strings.Contains(httpErr.Message, "quota exceeded") {
		t.Errorf("expected quota message, got %q", httpErr.Message)
	}
}

func TestDeleteDatabaseSnapshot_Success(t *testing.T) {
	h := &recordingHandler{response: `{
        "operationId": "snapshot-delete:mygame-prod-0-manual-20260409-153042",
        "type": "snapshot-delete",
        "status": "in-progress",
        "createdAt": "2026-04-09T15:30:42Z",
        "metadata": {"snapshotIdentifier": "mygame-prod-0-manual-20260409-153042"}
    }`}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	op, err := newTestTargetEnvironment(server.URL).DeleteDatabaseSnapshot("mygame-prod-0-manual-20260409-153042")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.method != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", h.method)
	}
	if h.path != "/v0/databases/test-env/snapshots/mygame-prod-0-manual-20260409-153042" {
		t.Errorf("unexpected path: %s", h.path)
	}
	if len(h.body) != 0 {
		t.Errorf("expected empty body, got %q", string(h.body))
	}
	if op.Type != DatabaseOperationTypeSnapshotDelete {
		t.Errorf("unexpected operation type: %s", op.Type)
	}
}

func TestRollbackDatabase_SendsBody(t *testing.T) {
	h := &recordingHandler{
		statusCode: http.StatusAccepted,
		response: `{
            "operationId": "rollback:mygame-prod-0:bt-abc123",
            "shardIndex": 0,
            "type": "rollback",
            "status": "pending",
            "createdAt": "2026-04-09T15:30:42Z",
            "metadata": {"backtrackIdentifier": "bt-abc123", "targetTime": "2026-04-09T15:00:00Z"}
        }`,
	}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	targetTime := time.Date(2026, 4, 9, 15, 0, 0, 0, time.UTC)
	op, err := newTestTargetEnvironment(server.URL).RollbackDatabase(&RollbackDatabaseRequest{
		ShardIndex: 0,
		TargetTime: targetTime,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.path != "/v0/databases/test-env/rollback" {
		t.Errorf("unexpected path: %s", h.path)
	}
	var sent RollbackDatabaseRequest
	if err := json.Unmarshal(h.body, &sent); err != nil {
		t.Fatalf("failed to decode sent body: %v", err)
	}
	if !sent.TargetTime.Equal(targetTime) {
		t.Errorf("unexpected target time sent: %v", sent.TargetTime)
	}
	if op.OperationID != "rollback:mygame-prod-0:bt-abc123" {
		t.Errorf("unexpected operation id: %s", op.OperationID)
	}
	if op.Status != DatabaseOperationStatusPending {
		t.Errorf("unexpected status: %s", op.Status)
	}
}

func TestRollbackDatabase_ConflictReturnsHTTPError(t *testing.T) {
	h := &recordingHandler{
		statusCode: http.StatusConflict,
		response:   `{"error":"cluster mygame-prod-0 is in \"backtracking\" state"}`,
	}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	_, err := newTestTargetEnvironment(server.URL).RollbackDatabase(&RollbackDatabaseRequest{ShardIndex: 0, TargetTime: time.Now().UTC()})
	var httpErr *metahttp.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 HTTPError, got %v", err)
	}
}

func TestListDatabaseOperations_Success(t *testing.T) {
	h := &recordingHandler{response: `{
        "operations": [
            {
                "operationId": "snapshot-create:mygame-prod-0-manual-20260409-153042",
                "shardIndex": 0,
                "type": "snapshot-create",
                "status": "in-progress",
                "createdAt": "2026-04-09T15:30:42Z"
            }
        ]
    }`}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	resp, err := newTestTargetEnvironment(server.URL).ListDatabaseOperations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.path != "/v0/databases/test-env/operations" {
		t.Errorf("unexpected path: %s", h.path)
	}
	if len(resp.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(resp.Operations))
	}
}

func TestGetDatabaseOperation_Success(t *testing.T) {
	h := &recordingHandler{response: `{
        "operationId": "snapshot-create:mygame-prod-0-manual-20260409-153042",
        "shardIndex": 0,
        "type": "snapshot-create",
        "status": "completed",
        "createdAt": "2026-04-09T15:30:42Z",
        "completedAt": "2026-04-09T15:32:11Z"
    }`}
	server := httptest.NewServer(h.handler())
	defer server.Close()

	op, err := newTestTargetEnvironment(server.URL).GetDatabaseOperation("snapshot-create:mygame-prod-0-manual-20260409-153042")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(h.path, "/operations/snapshot-create:mygame-prod-0-manual-20260409-153042") {
		t.Errorf("unexpected path: %s", h.path)
	}
	if !op.IsTerminal() {
		t.Error("expected IsTerminal to be true for completed operation")
	}
	if op.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}
}

func TestIsTerminal(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{DatabaseOperationStatusPending, false},
		{DatabaseOperationStatusInProgress, false},
		{DatabaseOperationStatusCompleted, true},
		{DatabaseOperationStatusFailed, true},
		{"", false},
	}
	for _, c := range cases {
		op := &DatabaseOperation{Status: c.status}
		if got := op.IsTerminal(); got != c.want {
			t.Errorf("status %q: IsTerminal() = %v, want %v", c.status, got, c.want)
		}
	}
}
