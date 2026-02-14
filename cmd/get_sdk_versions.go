/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type getSdkVersionsOpts struct {
	flagFormat string
}

func init() {
	o := getSdkVersionsOpts{}

	cmd := &cobra.Command{
		Use:   "sdk-versions",
		Short: "List available Metaplay SDK versions",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			List all available Metaplay SDK versions that can be downloaded from the portal.

			You must be logged in to the Metaplay portal (use 'metaplay auth login').
		`),
		Example: renderExample(`
			# List all available SDK versions in text format (default).
			metaplay get sdk-versions

			# List all available SDK versions in JSON format.
			metaplay get sdk-versions --format=json
		`),
	}

	getCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagFormat, "format", "text", "Output format: 'text' or 'json'")
}

func (o *getSdkVersionsOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate format
	if o.flagFormat != "text" && o.flagFormat != "json" {
		return fmt.Errorf("invalid format %q, must be either 'text' or 'json'", o.flagFormat)
	}

	return nil
}

func (o *getSdkVersionsOpts) Run(cmd *cobra.Command) error {
	// Ensure the user is logged in (Metaplay auth provider).
	authProvider := auth.NewMetaplayAuthProvider()
	tokenSet, err := tui.RequireLoggedIn(cmd.Context(), authProvider)
	if err != nil {
		return err
	}

	// Fetch SDK versions from the portal.
	portalClient := portalapi.NewClient(tokenSet)
	allVersions, err := portalClient.GetSdkVersions()
	if err != nil {
		return fmt.Errorf("failed to fetch SDK versions: %w", err)
	}

	// Filter to only include downloadable versions (those with StoragePath).
	var versions []portalapi.SdkVersionInfo
	for _, v := range allVersions {
		if v.StoragePath != nil {
			versions = append(versions, v)
		}
	}

	// Sort versions by semantic version (descending - newest first).
	sort.Slice(versions, func(i, j int) bool {
		vi, errI := version.NewVersion(versions[i].Version)
		vj, errJ := version.NewVersion(versions[j].Version)
		if errI != nil || errJ != nil {
			// Fall back to string comparison if parsing fails.
			return versions[i].Version > versions[j].Version
		}
		return vi.GreaterThan(vj)
	})

	// Output in desired format.
	if o.flagFormat == "json" {
		versionsJSON, err := json.MarshalIndent(versions, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal SDK versions as JSON: %w", err)
		}
		log.Info().Msg(string(versionsJSON))
	} else {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderTitle("SDK Versions"))
		log.Info().Msg("")

		if len(versions) == 0 {
			log.Info().Msg("No downloadable SDK versions found.")
		} else {
			// Print header
			log.Info().Msgf("  %-10s  %-12s  %s", "VERSION", "RELEASE DATE", "DESCRIPTION")
			log.Info().Msg("")

			for _, v := range versions {
				releaseDate := ""
				if v.ReleaseDate != nil {
					releaseDate = *v.ReleaseDate
				}

				description := ""
				if v.Description != nil {
					description = strings.ReplaceAll(*v.Description, "\n", " ")
					description = strings.TrimSpace(description)
				}

				// Add badges for non-public and test assets
				// Emojis are 2 chars wide in terminal, plus 1 space before each
				badges := ""
				badgeWidth := 0
				if !v.IsPublic {
					badges += " 🔒"
					badgeWidth += 3
				}
				if v.IsTestAsset {
					badges += " 🧪"
					badgeWidth += 3
				}

				// Pad version+badges to fixed column width (10 chars total)
				versionColWidth := 10
				padding := versionColWidth - len(v.Version) - badgeWidth
				if padding < 0 {
					padding = 0
				}
				versionWithBadges := styles.RenderTechnical(v.Version) + badges + strings.Repeat(" ", padding)
				datePadded := fmt.Sprintf("%-12s", releaseDate)

				log.Info().Msgf("  %s  %s  %s",
					versionWithBadges,
					styles.RenderMuted(datePadded),
					description)
			}
		}
		log.Info().Msg("")
	}

	return nil
}
