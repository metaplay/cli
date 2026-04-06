/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"

	"github.com/creativeprojects/go-selfupdate"
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

type selfupdateLogger struct{}

func (l selfupdateLogger) Print(v ...interface{}) {
	log.Debug().Msgf("%v", v...)
}

func (l selfupdateLogger) Printf(format string, v ...interface{}) {
	log.Debug().Msgf(format, v...)
}

func (o *updateCliOpts) Run(cmd *cobra.Command) error {
	selfupdate.SetLogger(selfupdateLogger{})

	prerelease := o.flagPrerelease || version.IsPrerelease() || version.IsDevBuild()
	if prerelease {
		log.Info().Msgf("Checking for the latest Metaplay CLI prerelease version...")
	} else {
		log.Info().Msgf("Checking for the latest Metaplay CLI version...")
	}

	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return fmt.Errorf("failed to initialize the Metaplay CLI updater source: %w", err)
	}

	var updateSource selfupdate.Source = source
	if prerelease {
		updateSource = &version.PrereleaseOnlySource{Inner: updateSource}
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:     updateSource,
		Prerelease: prerelease,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize the Metaplay CLI updater: %w", err)
	}

	latest, found, err := updater.DetectLatest(context.Background(), selfupdate.ParseSlug("metaplay/cli"))
	if err != nil {
		return fmt.Errorf("failed to detect the latest Metaplay CLI version: %w", err)
	}
	if !found {
		log.Info().Msgf("Already on the latest Metaplay CLI version (%s)", version.AppVersion)
		return nil
	}

	log.Info().Msgf("Downloading Metaplay CLI version %s...", styles.RenderTechnical(latest.Version()))

	// Calling vendored implementation of `GetExecutablePath()` due to a bug in `selfupdate.GetExecutablePath()`
	// that uses `filepath.EvalSymlinks()` known to be broken on Windows.
	// A PR has been made for the `go-selfupdate` library: https://github.com/creativeprojects/go-selfupdate/pull/46
	exe, err := pathutil.GetExecutablePath()
	if err != nil {
		return fmt.Errorf("could not determine the Metaplay CLI executable path: %w", err)
	}

	if err := updater.UpdateTo(context.Background(), latest, exe); err != nil {
		return fmt.Errorf("failed to update the Metaplay CLI binary: %w", err)
	}

	log.Info().Msg("")
	log.Info().Msgf(styles.RenderSuccess("✅ Successfully updated to version %s!"), latest.Version())

	return nil
}
