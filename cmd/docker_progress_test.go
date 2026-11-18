/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"errors"
	"slices"
	"testing"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/moby/moby/api/types/jsonstream"
)

// TestFollowDockerProgressError checks that an error reported inside the progress stream ends it and
// is returned as is, along with whether layer progress came before it.
func TestFollowDockerProgressError(t *testing.T) {
	streamErr := &jsonstream.Error{Message: "error from registry: requested access to the resource is denied"}
	tests := []struct {
		name              string
		before            []jsonstream.Message
		wantLayersStarted bool
	}{
		{name: "before any layer", before: []jsonstream.Message{{Status: "The push refers to repository [example.com/game]"}}},
		{name: "after layer progress", before: []jsonstream.Message{{ID: "1a2b3c4d5e6f", Status: "Downloading"}}, wantLayersStarted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := append(slices.Clone(tt.before), jsonstream.Message{Error: streamErr}, jsonstream.Message{Status: "never read"})
			read := 0
			layersStarted, err := followDockerProgress(&tui.TaskOutput{}, func(yield func(jsonstream.Message, error) bool) {
				for _, m := range messages {
					read++
					if !yield(m, nil) {
						return
					}
				}
			})
			if !errors.Is(err, streamErr) {
				t.Errorf("error = %v, want the stream error", err)
			}
			if read != len(tt.before)+1 {
				t.Errorf("read %d messages, want the stream to end at the error (%d)", read, len(tt.before)+1)
			}
			if layersStarted != tt.wantLayersStarted {
				t.Errorf("layersStarted = %v, want %v", layersStarted, tt.wantLayersStarted)
			}
		})
	}
}

// TestDockerTransferFailedSuggestion checks that the suggestion follows the registry's answer about
// access, and that the registry is not asked when the error already explains itself.
func TestDockerTransferFailedSuggestion(t *testing.T) {
	daemonErr := errors.New("error from registry: something went wrong")
	tests := []struct {
		name           string
		cancelled      bool
		err            error
		access         envapi.RegistryAccess
		wantSuggestion string
		noCheck        bool // No checkAccess is passed
		wantNoCheck    bool
	}{
		{name: "access denied", err: daemonErr, access: envapi.RegistryAccessDenied, wantSuggestion: envapi.RegistryCredentialsSuggestion},
		{name: "repository not found", err: daemonErr, access: envapi.RegistryRepositoryNotFound, wantSuggestion: "The environment's image repository was not found. Check the environment, and that its registry credentials can access the repository"},
		{name: "image not found", err: daemonErr, access: envapi.RegistryImageNotFound, wantSuggestion: "Check the tag. 'metaplay image list ENVIRONMENT' shows the environment's images"},
		{name: "access allowed", err: daemonErr, access: envapi.RegistryAccessUnknown},
		{name: "not about access", err: daemonErr, noCheck: true, wantNoCheck: true},
		{name: "cancelled", cancelled: true, err: context.Canceled, wantNoCheck: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelled {
				cancel()
			}
			checked := false
			checkAccess := func() envapi.RegistryAccess {
				checked = true
				return tt.access
			}
			if tt.noCheck {
				checkAccess = nil
			}
			err := dockerTransferFailed(ctx, tt.err, "pull", "example.com/game:1", checkAccess)
			if checked && tt.wantNoCheck {
				t.Error("asked the registry, want no check")
			}
			cliErr, ok := errors.AsType[*clierrors.CLIError](err)
			if !ok {
				t.Fatalf("error is %T, want *clierrors.CLIError", err)
			}
			if !errors.Is(err, tt.err) {
				t.Error("error does not wrap the daemon's error")
			}
			if cliErr.Suggestion != tt.wantSuggestion {
				t.Errorf("suggestion = %q, want %q", cliErr.Suggestion, tt.wantSuggestion)
			}
		})
	}
}
