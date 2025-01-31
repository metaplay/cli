/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/metaplay/cli/internal/version"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Show the version info of the application.
type VersionOpts struct {
	flagJsonOutput bool
}

func init() {
	o := VersionOpts{}

	var cmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version information of this CLI.",
		Run:   runCommand(&o),
	}

	rootCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.BoolVar(&o.flagJsonOutput, "json", false, "Output the version information as JSON")
}

func (o *VersionOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *VersionOpts) Run(cmd *cobra.Command) error {
	if o.flagJsonOutput {
		// Create structured version info with exported fields
		type VersionInfo struct {
			AppVersion string `json:"appVersion"`
			GitCommit  string `json:"gitCommit"`
			Prerelease bool   `json:"prerelease"`
		}
		info := VersionInfo{
			AppVersion: version.AppVersion,
			GitCommit:  version.GitCommit,
			Prerelease: version.IsDevBuild(),
		}

		// Marshal to JSON.
		infoJson, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal to JSON: %w", err)
		}

		log.Info().Msg(string(infoJson))
	} else {
		log.Info().Msgf("%s", version.AppVersion)
	}

	return nil
}
