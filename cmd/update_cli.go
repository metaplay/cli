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

type updateCliOpts struct{}

func init() {
	o := updateCliOpts{}

	var cmd = &cobra.Command{
		Use:   "cli",
		Short: "Update the Metaplay CLI to the latest version",
		Run:   runCommand(&o),
	}

	updateCmd.AddCommand(cmd)
}

func (o *updateCliOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *updateCliOpts) Run(cmd *cobra.Command) error {
	if version.IsDevBuild() {
		return fmt.Errorf("The update command is disabled on development builds!")
	}

	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return fmt.Errorf("Failed to initialize the Metaplay CLI updater source: %w", err)
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source: source,
	})
	if err != nil {
		return fmt.Errorf("Failed to initialize the Metaplay CLI updater: %w", err)
	}

	latest, found, err := updater.DetectLatest(context.Background(), selfupdate.ParseSlug("metaplay/cli"))
	if err != nil {
		return fmt.Errorf("Failed to detect the latest Metaplay CLI version: %w", err)
	}
	if !found {
		log.Info().Msgf("No newer Metaplay CLI version found")
		return nil
	}

	// Calling vendored implementation of `GetExecutablePath()` due to a bug in `selfupdate.GetExecutablePath()`
	// that uses `filepath.EvalSymlinks()` known to be broken on Windows.
	// A PR has been made for the `go-selfupdate` library: https://github.com/creativeprojects/go-selfupdate/pull/46
	exe, err := pathutil.GetExecutablePath()
	if err != nil {
		return fmt.Errorf("Could not determine the Metaplay CLI executable path: %w", err)
	}

	if err := updater.UpdateTo(context.Background(), latest, exe); err != nil {
		return fmt.Errorf("Failed to update the Metaplay CLI binary: %w", err)
	}

	log.Info().Msg("")
	log.Info().Msgf(styles.RenderSuccess("✅ Successfully updated to version %s!"), latest.Version())

	return nil
}
