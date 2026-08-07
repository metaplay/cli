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

// ErrLeftoverInUse reports that a temp file from an earlier update could not be removed even
// though the install itself is replaceable, which means another process is holding it.
//
// This must not be conflated with a permission problem. On Windows the file that update.Apply
// leaves behind after every successful swap is the mapped image of the process that performed
// it, and removing a mapped image fails with ERROR_ACCESS_DENIED — indistinguishable by errno
// from an unwritable install, but no amount of elevation can fix it.
var ErrLeftoverInUse = errors.New("a file left over from an earlier update could not be removed")

// updateTempSuffixes are the temp files that update.Apply creates next to the target
// executable while swapping the binary.
var updateTempSuffixes = []string{".new", ".old"}

// updateTempPaths returns the temp file paths update.Apply works with for the given target.
// These must match the library's own construction (Dir + Base + "."+name+suffix), or the
// cleanup below silently misses them.
func updateTempPaths(exePath string) []string {
	dir, name := filepath.Split(exePath)
	paths := make([]string, 0, len(updateTempSuffixes))
	for _, suffix := range updateTempSuffixes {
		paths = append(paths, filepath.Join(dir, "."+name+suffix))
	}
	return paths
}

// EnsureReplaceable clears anything blocking an in-place update of the executable at exePath
// and verifies that the swap can actually happen, so an unwritable install (a
// package-manager-managed or system-wide location) fails fast rather than after a download of
// tens of MB. It removes files, so it is not a read-only check.
//
// It probes what update.Apply does: create a file in the install directory, then rename over
// the target. It deliberately never opens the target for writing — on Windows a running
// executable is locked against writes but can still be renamed, which is how the swap works.
//
// Checks run cheapest and least destructive first, which also decides how failures are
// reported: an install that can never be updated says so, rather than blaming a leftover file
// that is merely a symptom.
func EnsureReplaceable(exePath string) error {
	// Probe that the existing executable may be renamed out of the way. Free and non-destructive,
	// so it goes first: on an install that can never self-update we then create nothing at all.
	if err := checkReplaceable(exePath); err != nil {
		return err
	}

	// Probe that files can be created in the install directory.
	dir := filepath.Dir(exePath)
	probe, err := os.CreateTemp(dir, ".metaplay-update-probe-*")
	if err != nil {
		return fmt.Errorf("cannot create files in %s: %w", pathutil.ForDisplay(dir), unwrapPathError(err))
	}
	_ = probe.Close()
	if err := os.Remove(probe.Name()); err != nil {
		// Not fatal — the checks that matter already passed — but it leaves a file behind, so
		// make it diagnosable instead of silent.
		log.Debug().Msgf("Failed to remove the probe file %s: %v", probe.Name(), err)
	}

	// Only now clear leftovers from an interrupted earlier update: Apply opens .<name>.new with
	// O_TRUNC and fails outright if it exists but is not writable, and on Windows renaming the
	// target to .old fails when .old already exists.
	return removeStaleUpdateFiles(exePath)
}

// removeStaleUpdateFiles deletes the temp files left next to exePath by an interrupted earlier
// update. Failures are reported as ErrLeftoverInUse rather than wrapping the underlying errno,
// so a mapped-image leftover can never be mistaken for an unwritable install; see
// ErrLeftoverInUse. Every suffix is attempted, so one blocked file does not hide another.
func removeStaleUpdateFiles(exePath string) error {
	var errs []error
	for _, path := range updateTempPaths(exePath) {
		err := os.Remove(path)
		switch {
		case err == nil:
			log.Debug().Msgf("Removed %s left over from an earlier update", path)
		case !errors.Is(err, fs.ErrNotExist):
			// %v, not %w, on the errno: keeping it out of the chain is the whole point
			// of ErrLeftoverInUse, and assertBlockedLeftover pins that contract.
			errs = append(errs, fmt.Errorf("%w: %s: %v",
				ErrLeftoverInUse, pathutil.ForDisplay(path), unwrapPathError(err)))
		}
	}
	return errors.Join(errs...)
}

// unwrapPathError reduces an *fs.PathError to its cause, so a message that already names the
// path via ForDisplay does not also print the raw \\?\-prefixed one the PathError carries.
func unwrapPathError(err error) error {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		return pathErr.Err
	}
	return err
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

	// Best-effort cleanup of leftovers from an interrupted earlier update; EnsureReplaceable
	// does this too, but Apply must not trip over them when called without a pre-flight check.
	if err := removeStaleUpdateFiles(exePath); err != nil {
		log.Debug().Msg(err.Error())
	}

	// Atomically replace the running executable (rename-with-rollback; Windows-safe).
	if err := update.Apply(binary, update.Options{TargetPath: exePath}); err != nil {
		return fmt.Errorf("failed to replace the executable: %w", err)
	}
	return nil
}
