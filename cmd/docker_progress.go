/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"iter"
	"os"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errhttp"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client/pkg/jsonmessage"
	"golang.org/x/term"
)

// followDockerProgress consumes the progress messages of an image pull or push until the stream
// ends, showing each layer's latest status in the task output's footer in interactive mode. It
// returns the first stream or daemon error. A stream from the client's JSONMessages ends when the
// response ends or its context is cancelled.
func followDockerProgress(output *tui.TaskOutput, messages iter.Seq2[jsonstream.Message, error]) error {
	progressIDs := []string{}                     // Layer IDs in the order they first appeared
	progresses := map[string]jsonstream.Message{} // Latest message for each layer ID

	for progress, err := range messages {
		if err != nil {
			return err
		}
		if progress.Error != nil {
			return newStreamError(progress.Error)
		}

		if progress.ID != "" {
			if _, exists := progresses[progress.ID]; !exists {
				progressIDs = append(progressIDs, progress.ID)
			}
			progresses[progress.ID] = progress
		}

		if tui.IsInteractiveMode() {
			updateDockerProgressOutput(output, progressIDs, progresses, dockerProgressWidth())
		}
	}
	return nil
}

// updateDockerProgressOutput shows the latest status of each layer in the task output's footer,
// with progress bars sized for a terminal width columns wide. The width is read on every update,
// so the bars follow a terminal resize.
func updateDockerProgressOutput(output *tui.TaskOutput, progressIDs []string, progresses map[string]jsonstream.Message, width uint16) {
	lines := []string{}

	for _, id := range progressIDs {
		progress := progresses[id]
		if progress.Progress == nil && progress.Status == "" {
			continue
		}

		// Shorten common verbose statuses
		status := progress.Status
		switch {
		case strings.HasPrefix(status, "Pulling fs layer"):
			status = "Pulling layer"
		case strings.HasPrefix(status, "Waiting"):
			status = "Waiting"
		case strings.HasPrefix(status, "Downloading"):
			status = "Downloading"
		case strings.HasPrefix(status, "Verifying Checksum"):
			status = "Verifying"
		case strings.HasPrefix(status, "Download complete"):
			status = "Downloaded"
		case strings.HasPrefix(status, "Extracting"):
			status = "Extracting"
		case strings.HasPrefix(status, "Pull complete"):
			status = "Complete"
		}

		progressLine := fmt.Sprintf("Layer %s: %s", id[:min(12, len(id))], status)
		if progress.Progress != nil {
			progressLine += fmt.Sprintf(" %s", jsonmessage.RenderTUIProgress(*progress.Progress, width))
		}
		lines = append(lines, strings.TrimSpace(progressLine))
	}

	output.SetFooterLines(lines)
}

// dockerProgressWidth is the width of the terminal the TUI draws on (stdout), or 200 when
// that is unknown, as moby's own progress display does. RenderTUIProgress draws the
// progress bar above 110 columns and the time left above 50.
func dockerProgressWidth() uint16 {
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && width > 0 {
		return uint16(min(width, 0xffff))
	}
	return 200
}

// dockerTransferFailed is the error for a failed image pull or push, where action is "pull" or
// "push". The daemon's message says what went wrong (missing tag or repository, denied access, no
// match for this platform, registry errors, disk full), so it is shown as the cause. The daemon
// reports a missing image, and a registry that hides private repositories, as not found.
func dockerTransferFailed(err error, action, imageName string) error {
	transferErr := clierrors.Wrapf(err, "Failed to %s image '%s'", action, imageName)
	switch {
	case cerrdefs.IsNotFound(err) && strings.Contains(err.Error(), "no matching manifest"):
		return transferErr // Image not built for this platform: the message says so, and the tag is right
	case cerrdefs.IsNotFound(err):
		return transferErr.WithSuggestion("Check the tag, the environment and its registry credentials")
	case cerrdefs.IsUnauthorized(err), cerrdefs.IsPermissionDenied(err), isRegistryAuthMessage(err.Error()):
		return transferErr.WithSuggestion("Check the environment's registry credentials")
	}
	return transferErr
}

// streamError is an error the daemon reported inside the progress stream with a status code. It
// gets the error type of that code, as errors returned before the stream starts do, and as the
// client's own Stream.Wait does.
//
// Most stream errors have no code: the daemon formats them with streamformatter.FormatError, which
// sets only the message. That includes every push failure, because the daemon writes a push's first
// progress message before it contacts the registry. Registry authentication failures are therefore
// also recognized by their message, in dockerTransferFailed.
type streamError struct {
	err    *jsonstream.Error
	errdef error
}

func newStreamError(err *jsonstream.Error) error {
	if err.Code == 0 {
		return err // No status code to derive a type from
	}
	return &streamError{err: err, errdef: errhttp.ToNative(err.Code)}
}

func (e *streamError) Error() string   { return e.err.Error() }
func (e *streamError) Unwrap() []error { return []error{e.err, e.errdef} }

// registryAuthFragments are registry authentication and authorization failures, as they appear in
// the daemon's messages. They come from the registry API's error codes ("denied: ...",
// "unauthorized: ..."), often after a request or context prefix; from registries' own wording
// (Docker Hub, ECR, Google Artifact Registry), which the containerd image store passes on after
// "error from registry: "; from the legacy image store ("no basic auth credentials"); and from
// containerd's resolver ("push access denied, ...", "failed to authorize: ...").
var registryAuthFragments = []string{
	"denied:",
	"unauthorized:",
	"access denied",
	"access to the resource is denied",
	"denied on resource",
	"not authorized to perform",
	"authorization token has expired",
	"incorrect username or password",
	"no basic auth credentials",
	"authentication required",
	"failed to authorize",
	"authorization failed",
}

// isRegistryAuthMessage reports whether message is a registry's authentication or authorization
// failure. A 401 or 403 counts only from containerd's resolver, which talks to the registry
// ("unexpected status from HEAD request to ...: 403 Forbidden"). A 403 from the blob storage a
// registry redirects layer downloads to (an expired presigned URL) is not a credentials problem.
func isRegistryAuthMessage(message string) bool {
	message = strings.ToLower(message)
	for _, fragment := range registryAuthFragments {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return strings.Contains(message, "unexpected status from") &&
		(strings.Contains(message, "401 unauthorized") || strings.Contains(message, "403 forbidden"))
}
