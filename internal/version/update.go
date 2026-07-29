/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/creativeprojects/go-selfupdate/update"
	"github.com/metaplay/cli/internal/pathutil"
	"github.com/rs/zerolog/log"
)

// updateTempSuffixes are the temp files that update.Apply creates next to the target
// executable while swapping the binary.
var updateTempSuffixes = []string{".new", ".old"}

// CheckWritable verifies that the executable at exePath can actually be replaced in place,
// so that an unwritable install (a package-manager-managed or system-wide location) fails
// fast instead of only after a download of tens of MB.
//
// It probes what update.Apply does: create a file in the install directory, then rename over
// the target. It deliberately never opens the target for writing — on Windows a running
// executable is locked against writes but can still be renamed, which is how the swap works.
func CheckWritable(exePath string) error {
	// Clear leftovers from an interrupted earlier update first: Apply opens .<name>.new with
	// O_TRUNC and fails outright if that file exists but is not writable by the current user
	// (eg, left behind by a run with elevated privileges).
	if err := removeStaleUpdateFiles(exePath); err != nil {
		return err
	}

	// Probe that files can be created in the install directory.
	dir := filepath.Dir(exePath)
	probe, err := os.CreateTemp(dir, ".metaplay-update-probe-*")
	if err != nil {
		return fmt.Errorf("cannot create files in %s: %w", pathutil.ForDisplay(dir), err)
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())

	// Probe that the existing executable may be renamed out of the way.
	return checkReplaceable(exePath)
}

// removeStaleUpdateFiles deletes .<name>.new / .<name>.old files left next to exePath by an
// interrupted earlier update. A leftover that cannot be removed is reported as an error,
// because Apply would fail on it later anyway: it truncates .new, and on Windows renaming
// the target to .old fails when .old already exists.
func removeStaleUpdateFiles(exePath string) error {
	dir, name := filepath.Split(exePath)
	for _, suffix := range updateTempSuffixes {
		path := filepath.Join(dir, "."+name+suffix)
		err := os.Remove(path)
		switch {
		case err == nil:
			log.Debug().Msgf("Removed %s left over from an earlier update", path)
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("failed to remove %s left over from an earlier update: %w", pathutil.ForDisplay(path), err)
		}
	}
	return nil
}

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

	// Best-effort cleanup of leftovers from an interrupted earlier update; CheckWritable does
	// this too, but Apply must not trip over them when called without a pre-flight check.
	if err := removeStaleUpdateFiles(exePath); err != nil {
		log.Debug().Msgf("%v", err)
	}

	// Atomically replace the running executable (rename-with-rollback; Windows-safe).
	if err := update.Apply(binary, update.Options{TargetPath: exePath}); err != nil {
		return fmt.Errorf("failed to replace the executable: %w", err)
	}
	return nil
}
