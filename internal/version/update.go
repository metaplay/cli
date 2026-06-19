/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"context"
	"fmt"
	"net/http"
	"runtime"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/creativeprojects/go-selfupdate/update"
)

// DownloadAndApply downloads the release archive for the given version from the GitHub
// CDN (not the throttled api.github.com), extracts the 'metaplay' binary, and atomically
// replaces the executable at exePath.
//
// It reuses go-selfupdate's standalone helpers for the archive handling and the safe,
// cross-platform binary swap, so we don't have to reimplement either.
//
// The archive is tens of MB, so this deliberately does NOT impose a hard timeout (a slow
// connection should not fail a legitimate update). Cancellation is governed by ctx, so the
// caller can bound or interrupt it (e.g. Ctrl+C via the command context).
func DownloadAndApply(ctx context.Context, tag, exePath string) error {
	url := assetURL(tag)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download %s: unexpected status %d", url, resp.StatusCode)
	}

	// Extract the 'metaplay' binary from the archive (format detected from the URL suffix).
	binary, err := selfupdate.DecompressCommand(resp.Body, url, "metaplay", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("failed to extract the binary from %s: %w", url, err)
	}

	// Atomically replace the running executable (rename-with-rollback; Windows-safe).
	if err := update.Apply(binary, update.Options{TargetPath: exePath}); err != nil {
		return fmt.Errorf("failed to replace the executable: %w", err)
	}
	return nil
}
