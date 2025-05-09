/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
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
type versionOpts struct {
	flagFormat string
}

var versionOpt = versionOpts{}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version information of this CLI",
	Run:   runCommand(&versionOpt),
}

func init() {
	rootCmd.AddCommand(versionCmd)

	flags := versionCmd.Flags()
	flags.StringVar(&versionOpt.flagFormat, "format", "text", "Output format. Valid values are 'text' or 'json'")
}

func (o *versionOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate format
	if o.flagFormat != "text" && o.flagFormat != "json" {
		return fmt.Errorf("invalid format %q, must be either 'text' or 'json'", o.flagFormat)
	}
	return nil
}

func (o *versionOpts) Run(cmd *cobra.Command) error {
	if o.flagFormat == "json" {
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
		infoJSON, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal to JSON: %w", err)
		}

		log.Info().Msg(string(infoJSON))
	} else {
		log.Info().Msgf("%s", version.AppVersion)
	}

	return nil
}
