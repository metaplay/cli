/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"io/fs"
	"runtime"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/pathutil"
	"github.com/metaplay/cli/internal/version"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// manualDownloadSuggestion is shown when an update fails for a reason the user can work
// around by fetching a release themselves.
const manualDownloadSuggestion = "Check your network connection, or download a release manually from https://github.com/metaplay/cli/releases"

// notWritableSuggestion is shown when the CLI cannot replace its own binary. Package manager
// installs live in directories that only root/Administrator may write to, so the update has
// to go through the package manager to keep its bookkeeping in sync.
const notWritableSuggestion = "If you installed the CLI with a package manager, update it with that instead; otherwise re-run from an elevated shell (Administrator or sudo)"

// fileInUseSuggestion is shown when the swap is blocked by something other than permissions,
// which in practice means a leftover file that another metaplay process still has open.
const fileInUseSuggestion = "Make sure no other metaplay process is running, then try again"

// replaceFailureError wraps a failure to replace the CLI binary, picking the hint from the
// cause: only a permission wall warrants pointing at package managers or elevation. Anything
// else keeps fallbackSuggestion, so the hint never misdescribes the problem.
func replaceFailureError(err error, message, exe, fallbackSuggestion string) *clierrors.CLIError {
	cliErr := clierrors.Wrap(err, message)
	if errors.Is(err, fs.ErrPermission) {
		return cliErr.WithSuggestion(notWritableSuggestion).
			WithDetails(notWritableDetails(exe)...)
	}
	return cliErr.WithSuggestion(fallbackSuggestion)
}

// notWritableDetails lists the install location and the package manager update commands that
// apply on this platform.
func notWritableDetails(exe string) []string {
	details := []string{"Installed at: " + pathutil.ForDisplay(exe)}
	if runtime.GOOS == "windows" {
		return append(details,
			"Chocolatey install: choco upgrade metaplay",
			"Scoop install: scoop update metaplay",
		)
	}
	return append(details, "Homebrew install: brew upgrade metaplay")
}

type updateCliOpts struct {
	flagPrerelease bool
}

func init() {
	o := updateCliOpts{}

	var cmd = &cobra.Command{
		Use:   "cli",
		Short: "Update the Metaplay CLI to the latest version",
		Run:   runCommand(&o),
	}

	cmd.Flags().BoolVar(&o.flagPrerelease, "prerelease", false, "Update to the latest prerelease version")

	updateCmd.AddCommand(cmd)
}

func (o *updateCliOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *updateCliOpts) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	prerelease := o.flagPrerelease || version.IsPrerelease() || version.IsDevBuild()
	if prerelease {
		log.Info().Msgf("Checking for the latest Metaplay CLI prerelease version...")
	} else {
		log.Info().Msgf("Checking for the latest Metaplay CLI version...")
	}

	// Detect the latest version via the non-throttled github.com endpoints (see internal/version/detect.go).
	latest, err := version.DetectLatest(ctx, prerelease)
	if err != nil {
		return clierrors.Wrap(err, "Failed to detect the latest Metaplay CLI version").
			WithSuggestion(manualDownloadSuggestion)
	}

	// A local "dev" build has no parseable version, so IsNewer can't compare it; always
	// proceed in that case so `update cli` can move a locally built binary onto a release.
	if !version.IsDevBuild() && !version.IsNewer(latest, version.AppVersion) {
		log.Info().Msgf("Already on the latest Metaplay CLI version (%s)", version.AppVersion)
		return nil
	}

	// Calling vendored implementation of `GetExecutablePath()` due to a bug in `selfupdate.GetExecutablePath()`
	// that uses `filepath.EvalSymlinks()` known to be broken on Windows.
	// A PR has been made for the `go-selfupdate` library: https://github.com/creativeprojects/go-selfupdate/pull/46
	exe, err := pathutil.GetExecutablePath()
	if err != nil {
		return clierrors.Wrap(err, "Could not determine the Metaplay CLI executable path")
	}

	// Check that the binary can be replaced before downloading tens of MB that we could not apply.
	if err := version.EnsureReplaceable(exe); err != nil {
		return replaceFailureError(err, "Cannot replace the Metaplay CLI binary", exe, fileInUseSuggestion)
	}

	log.Info().Msgf("Downloading Metaplay CLI version %s...", styles.RenderTechnical(latest))

	// The swap can still hit a permission wall that the pre-flight check could not see, so the
	// network hint only applies when the failure is not about permissions.
	if err := version.DownloadAndApply(ctx, latest, exe); err != nil {
		return replaceFailureError(err, "Failed to update the Metaplay CLI binary", exe, manualDownloadSuggestion)
	}

	log.Info().Msg("")
	log.Info().Msgf(styles.RenderSuccess("✅ Successfully updated to version %s!"), latest)

	return nil
}
