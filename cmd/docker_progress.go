/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"
	"iter"
	"os"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/jsonmessage"
	"golang.org/x/term"
)

// followDockerProgress consumes the progress messages of an image pull or push until the stream
// ends, showing each layer's latest status in the task output's footer in interactive mode. It
// returns the first stream or daemon error, and whether any layer progress came before it. A
// stream from the client's JSONMessages ends when the response ends or its context is cancelled.
func followDockerProgress(output *tui.TaskOutput, messages iter.Seq2[jsonstream.Message, error]) (layersStarted bool, err error) {
	progressIDs := []string{}                     // Layer IDs in the order they first appeared
	progresses := map[string]jsonstream.Message{} // Latest message for each layer ID

	for progress, err := range messages {
		if err != nil {
			return len(progressIDs) > 0, err
		}
		if progress.Error != nil {
			return len(progressIDs) > 0, progress.Error
		}

		if progress.ID != "" {
			if _, exists := progresses[progress.ID]; !exists {
				progressIDs = append(progressIDs, progress.ID)
			}
			progresses[progress.ID] = progress
		}

		if tui.IsInteractiveMode() {
			updateDockerProgressOutput(output, progressIDs, progresses)
		}
	}
	return len(progressIDs) > 0, nil
}

// updateDockerProgressOutput shows the latest status of each layer in the task output's footer.
// The terminal width is read on every update, so the progress bars follow a resize.
func updateDockerProgressOutput(output *tui.TaskOutput, progressIDs []string, progresses map[string]jsonstream.Message) {
	lines := []string{}
	width := dockerProgressWidth()

	for _, id := range progressIDs {
		progress := progresses[id]
		if progress.Progress == nil && progress.Status == "" {
			continue
		}

		// Docker's own status wording, so it matches what 'docker pull' and 'docker push' show.
		progressLine := fmt.Sprintf("Layer %s: %s", id[:min(12, len(id))], progress.Status)
		if progress.Progress != nil {
			// The progress gets what the line's prefix and the task output's indent leave.
			used := min(width, tui.TaskOutputIndent+len(progressLine)+1)
			progressLine += " " + jsonmessage.RenderTUIProgress(*progress.Progress, uint16(min(width-used, 0xffff)))
		}
		lines = append(lines, strings.TrimSpace(progressLine))
	}

	output.SetFooterLines(lines)
}

// dockerProgressWidth is the width of the terminal the TUI draws on (stdout), or 80 columns when
// that is unknown, so that lines fit a default-size terminal. RenderTUIProgress draws the progress
// bar when given more than 110 columns, and the time left when given more than 50.
func dockerProgressWidth() int {
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && width > 0 {
		return width
	}
	return 80
}

// dockerTransferFailed is the error for a failed image pull or push, where action is "pull" or
// "push". The daemon's message says what went wrong, so it is shown as the cause. For a suggestion,
// the registry is asked about access with checkAccess, because the daemon's error often has no
// status code (it never has for a push) and its wording depends on the registry and image store.
// checkAccess is nil when the failure can't be about access.
func dockerTransferFailed(ctx context.Context, err error, action, imageName string, checkAccess func() envapi.RegistryAccess) error {
	transferErr := clierrors.Wrapf(err, "Failed to %s image '%s'", action, imageName)
	switch {
	case ctx.Err() != nil:
		return transferErr // Cancelled (runCommand reports cancellation)
	case client.IsErrConnectionFailed(err):
		return transferErr.WithSuggestion("Make sure Docker is running, then try again")
	case checkAccess == nil:
		return transferErr
	}
	switch checkAccess() {
	case envapi.RegistryAccessDenied:
		return transferErr.WithSuggestion(envapi.RegistryCredentialsSuggestion)
	case envapi.RegistryRepositoryNotFound:
		return transferErr.WithSuggestion("The environment's image repository was not found. Check the environment, and that its registry credentials can access the repository")
	case envapi.RegistryImageNotFound:
		return transferErr.WithSuggestion("Check the tag. 'metaplay image list ENVIRONMENT' shows the environment's images")
	}
	return transferErr
}
