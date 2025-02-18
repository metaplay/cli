/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"context"
	"fmt"

	"github.com/creativeprojects/go-selfupdate"
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
		return fmt.Errorf("Failed to initialize the Metaplay CLI updater source")
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source: source,
	})
	if err != nil {
		return fmt.Errorf("Failed to initialize the Metaplay CLI updater")
	}

	latest, found, err := updater.DetectLatest(context.Background(), selfupdate.ParseSlug("metaplay/cli"))
	if err != nil {
		return fmt.Errorf("Failed to detect the latest Metaplay CLI version")
	}
	if !found {
		log.Info().Msgf("No newer Metaplay CLI version found")
		return nil
	}

	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("Could not determine the Metaplay CLI executable path")
	}

	if err := updater.UpdateTo(context.Background(), latest, exe); err != nil {
		return fmt.Errorf("Failed to update the Metaplay CLI binary")
	}

	log.Info().Msg("")
	log.Info().Msgf(styles.RenderSuccess("✅ Successfully updated to version %s!"), latest.Version())

	return nil
}
