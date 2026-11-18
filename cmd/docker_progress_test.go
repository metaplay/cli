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

// TestFollowDockerProgressErrorType checks that an error reported inside the progress stream gets
// an error type, so that pull and push failures can choose a suggestion. The daemon sends push
// errors without a status code, so registry authentication failures are typed by their message.
func TestFollowDockerProgressErrorType(t *testing.T) {
	tests := []struct {
		name           string
		code           int
		message        string
		isNotFound     bool
		isUnauthorized bool
	}{
		{name: "status not found", code: 404, message: "manifest unknown", isNotFound: true},
		{name: "status unauthorized", code: 401, message: "authentication required", isUnauthorized: true},
		{name: "registry denied", message: "denied: requested access to the resource is denied", isUnauthorized: true},
		{name: "ecr token expired", message: "denied: Your authorization token has expired. Reauthenticate and try again.", isUnauthorized: true},
		{name: "registry unauthorized", message: "unauthorized: authentication required", isUnauthorized: true},
		{name: "no credentials", message: "no basic auth credentials", isUnauthorized: true},
		{name: "containerd push denied", message: "push access denied, repository does not exist or may require authorization: server message: insufficient_scope: authorization failed", isUnauthorized: true},
		{name: "containerd authorize", message: "failed to authorize: failed to fetch oauth token: unexpected status: 401 Unauthorized", isUnauthorized: true},
		{name: "other error", message: "write /var/lib/docker/tmp/x: no space left on device"},
		{name: "local permission", message: "open /var/lib/docker/image/x: permission denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streamErr := &jsonstream.Error{Code: tt.code, Message: tt.message}
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

// TestDockerTransferFailedSuggestion checks which failures get a suggestion. A platform mismatch
// is reported as not found, but the tag and credentials are right, so it gets none.
func TestDockerTransferFailedSuggestion(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantSuggestion string
	}{
		{name: "not found", err: cerrdefs.ErrNotFound.WithMessage("manifest unknown"), wantSuggestion: "Check the tag, the environment and its registry credentials"},
		{name: "platform mismatch", err: cerrdefs.ErrNotFound.WithMessage("no matching manifest for linux/arm64 in the manifest list entries")},
		{name: "unauthorized", err: newStreamError(&jsonstream.Error{Message: "denied: requested access to the resource is denied"}), wantSuggestion: "Check the environment's registry credentials"},
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
