/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/pathutil"
	"github.com/metaplay/cli/internal/version"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

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
			WithSuggestion("Check your network connection, or download a release manually from https://github.com/metaplay/cli/releases")
	}

	if !version.IsNewer(latest, version.AppVersion) {
		log.Info().Msgf("Already on the latest Metaplay CLI version (%s)", version.AppVersion)
		return nil
	}

	log.Info().Msgf("Downloading Metaplay CLI version %s...", styles.RenderTechnical(latest))

	// Calling vendored implementation of `GetExecutablePath()` due to a bug in `selfupdate.GetExecutablePath()`
	// that uses `filepath.EvalSymlinks()` known to be broken on Windows.
	// A PR has been made for the `go-selfupdate` library: https://github.com/creativeprojects/go-selfupdate/pull/46
	exe, err := pathutil.GetExecutablePath()
	if err != nil {
		return clierrors.Wrap(err, "Could not determine the Metaplay CLI executable path")
	}

	if err := version.DownloadAndApply(ctx, latest, exe); err != nil {
		return clierrors.Wrap(err, "Failed to update the Metaplay CLI binary").
			WithSuggestion("Check your network connection, or download a release manually from https://github.com/metaplay/cli/releases")
	}

	log.Info().Msg("")
	log.Info().Msgf(styles.RenderSuccess("✅ Successfully updated to version %s!"), latest)

	return nil
}
