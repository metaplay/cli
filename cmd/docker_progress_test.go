/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/moby/moby/api/types/jsonstream"
)

// TestFollowDockerProgressErrorType checks that an error reported inside the progress stream with a
// status code gets the error type of that code, and one without a code stays untyped.
func TestFollowDockerProgressErrorType(t *testing.T) {
	tests := []struct {
		name           string
		code           int
		isNotFound     bool
		isUnauthorized bool
	}{
		{name: "not found", code: 404, isNotFound: true},
		{name: "unauthorized", code: 401, isUnauthorized: true},
		{name: "no status code", code: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streamErr := &jsonstream.Error{Code: tt.code, Message: "registry error"}
			messages := []jsonstream.Message{{Status: "The push refers to repository [example.com/game]"}, {Error: streamErr}}
			err := followDockerProgress(&tui.TaskOutput{}, func(yield func(jsonstream.Message, error) bool) {
				for _, m := range messages {
					if !yield(m, nil) {
						return
					}
				}
			})
			if err == nil {
				t.Fatal("followDockerProgress succeeded, want an error")
			}
			if err.Error() != streamErr.Message {
				t.Errorf("message = %q, want %q", err.Error(), streamErr.Message)
			}
			if !errors.Is(err, streamErr) {
				t.Error("error does not wrap the stream error")
			}
			if got := cerrdefs.IsNotFound(err); got != tt.isNotFound {
				t.Errorf("IsNotFound = %v, want %v", got, tt.isNotFound)
			}
			if got := cerrdefs.IsUnauthorized(err); got != tt.isUnauthorized {
				t.Errorf("IsUnauthorized = %v, want %v", got, tt.isUnauthorized)
			}
		})
	}
}

// TestIsRegistryAuthMessage checks registry authentication failures in the forms the legacy and
// containerd image stores report them, and messages that only look similar.
func TestIsRegistryAuthMessage(t *testing.T) {
	tests := []struct {
		message string
		want    bool
	}{
		// Legacy image store
		{"denied: requested access to the resource is denied", true},
		{"denied: Your authorization token has expired. Reauthenticate and try again.", true},
		{"unauthorized: authentication required", true},
		{"no basic auth credentials", true},
		{`Head "https://registry.example.com/v2/game/manifests/1": unauthorized: incorrect username or password`, true},
		{"error pulling image configuration: download failed after attempts=1: unauthorized: authentication required", true},
		// Containerd image store
		{"error from registry: requested access to the resource is denied", true},
		{"error from registry: User: arn:aws:iam::123456789012:user/ci is not authorized to perform: ecr:InitiateLayerUpload on resource: arn:aws:ecr:eu-west-1:123456789012:repository/game", true},
		{`error from registry: Permission "artifactregistry.repositories.uploadArtifacts" denied on resource "projects/p/locations/l/repositories/r"`, true},
		{"push access denied, repository does not exist or may require authorization: server message: insufficient_scope: authorization failed", true},
		{"failed to authorize: failed to fetch oauth token: unexpected status: 401 Unauthorized", true},
		{"unknown: failed to resolve reference \"x.dkr.ecr.eu-west-1.amazonaws.com/game:1\": unexpected status from HEAD request to https://x.dkr.ecr.eu-west-1.amazonaws.com/v2/game/manifests/1: 403 Forbidden", true},
		{"authorization failed: no basic auth credentials", true},
		// Not authentication failures
		{"error from registry: toomanyrequests: You have reached your pull rate limit", false},
		{"failed to copy: httpReadSeeker: failed open: unexpected status code https://bucket.s3.amazonaws.com/layer?X-Amz-Signature=x: 403 Forbidden", false},
		{"write /var/lib/docker/tmp/x: no space left on device", false},
		{"open /var/lib/docker/image/x: permission denied", false},
		{"no matching manifest for linux/arm64 in the manifest list entries", false},
	}
	for _, tt := range tests {
		if got := isRegistryAuthMessage(tt.message); got != tt.want {
			t.Errorf("isRegistryAuthMessage(%q) = %v, want %v", tt.message, got, tt.want)
		}
	}
}

// TestDockerTransferFailedSuggestion checks which failures get which suggestion, whether the error
// came before the stream (typed by HTTP status) or inside it.
func TestDockerTransferFailedSuggestion(t *testing.T) {
	const (
		checkTag         = "Check the tag, the environment and its registry credentials"
		checkCredentials = "Check the environment's registry credentials"
	)
	tests := []struct {
		name           string
		err            error
		wantSuggestion string
	}{
		{name: "not found", err: cerrdefs.ErrNotFound.WithMessage("manifest unknown"), wantSuggestion: checkTag},
		{name: "platform mismatch", err: cerrdefs.ErrNotFound.WithMessage("no matching manifest for linux/arm64 in the manifest list entries")},
		{name: "typed unauthorized", err: cerrdefs.ErrUnauthenticated.WithMessage("authentication required"), wantSuggestion: checkCredentials},
		{name: "typed permission denied", err: cerrdefs.ErrPermissionDenied.WithMessage("denied"), wantSuggestion: checkCredentials},
		{
			name:           "untyped auth failure before the stream",
			err:            cerrdefs.ErrInternal.WithMessage("unknown: failed to resolve reference: unexpected status from HEAD request to https://registry.example.com/v2/game/manifests/1: 403 Forbidden"),
			wantSuggestion: checkCredentials,
		},
		{
			name:           "auth failure in the stream without a code",
			err:            newStreamError(&jsonstream.Error{Message: "error from registry: requested access to the resource is denied"}),
			wantSuggestion: checkCredentials,
		},
		{
			name:           "auth failure in the stream with a non-auth code",
			err:            newStreamError(&jsonstream.Error{Code: 500, Message: "denied: requested access to the resource is denied"}),
			wantSuggestion: checkCredentials,
		},
		{name: "blob storage 403", err: errors.New("failed to copy: httpReadSeeker: failed open: unexpected status code https://bucket.s3.amazonaws.com/x: 403 Forbidden")},
		{name: "other", err: errors.New("no space left on device")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := dockerTransferFailed(tt.err, "pull", "example.com/game:1")
			cliErr, ok := errors.AsType[*clierrors.CLIError](err)
			if !ok {
				t.Fatalf("error is %T, want *clierrors.CLIError", err)
			}
			if cliErr.Suggestion != tt.wantSuggestion {
				t.Errorf("suggestion = %q, want %q", cliErr.Suggestion, tt.wantSuggestion)
			}
		})
	}
}
