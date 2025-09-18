/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// initSdkOpts holds options for the 'metaplay init sdk' command.
type initSdkOpts struct {
	flagSdkVersion         string // Required: Metaplay SDK version (e.g., "34.0") or version name
	flagSdkDirectory       string // Optional when metaplay-project.yaml is present: directory where MetaplaySDK/ should be created
	flagAutoAgreeContracts bool   // Automatically agree to the terms & conditions.
}

func init() {
	o := &initSdkOpts{}

	cmd := &cobra.Command{
		Use:   "sdk",
		Short: "Download the Metaplay SDK into a target directory",
		Run:   runCommand(o),
		Long: renderLong(o, `
			Download and extract a specific Metaplay SDK release to a directory.

			The SDK will be extracted to the directory specified by --sdk-directory (defaults to "MetaplaySDK" in the current directory).
			The extractor creates a MetaplaySDK/ directory containing all SDK files.

			You must be logged in to the Metaplay portal (use 'metaplay auth login') and have agreed to the privacy policy
			and terms & conditions in the portal (https://portal.metaplay.dev).
		`),
		Example: renderExample(`
			# Download SDK v34.0 into the current directory (creates ./MetaplaySDK/)
			metaplay init sdk --sdk-version=34.0

			# Download SDK v34.0 into a custom directory (creates ../Shared/MetaplaySDK/)
			metaplay init sdk --sdk-version=34.0 --sdk-directory=../Shared/MetaplaySDK
		`),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagSdkVersion, "sdk-version", "", "Metaplay SDK version to download (required)")
	flags.StringVar(&o.flagSdkDirectory, "sdk-directory", "MetaplaySDK", "Directory where MetaplaySDK/ should be created")
	flags.BoolVar(&o.flagAutoAgreeContracts, "auto-agree", false, "Automatically agree to the privacy policy and terms and conditions")

	initCmd.AddCommand(cmd)
}

func (o *initSdkOpts) Prepare(cmd *cobra.Command, args []string) error {
	if o.flagSdkVersion == "" {
		return fmt.Errorf("--sdk-version is required")
	}
	return nil
}

func (o *initSdkOpts) Run(cmd *cobra.Command) error {
	// Resolve the absolute directory into which the SDK will be extracted.
	// The path is always specified via --sdk-directory (with default "MetaplaySDK").
	abs, err := filepath.Abs(o.flagSdkDirectory)
	if err != nil {
		return fmt.Errorf("failed to resolve --sdk-directory: %w", err)
	}

	// Assert that the last segment is 'MetaplaySDK'
	if filepath.Base(abs) != "MetaplaySDK" {
		return fmt.Errorf("--sdk-directory must point to a 'MetaplaySDK' directory, got: %q", o.flagSdkDirectory)
	}

	targetSdkDirAbs := abs
	parentDir := filepath.Dir(targetSdkDirAbs)

	// Ensure the user is logged in (Metaplay auth provider).
	authProvider := auth.NewMetaplayAuthProvider()
	tokenSet, err := tui.RequireLoggedIn(cmd.Context(), authProvider)
	if err != nil {
		return err
	}

	// Agree to contracts if needed (required for SDK download)
	portalClient := portalapi.NewClient(tokenSet)
	if err := ensureSdkDownloadContractsAccepted(cmd.Context(), portalClient, o.flagAutoAgreeContracts); err != nil {
		return err
	}

	// Resolve SDK version info (must exist and be downloadable)
	sdkVersionInfo, err := portalClient.FindSdkVersionByVersionOrName(o.flagSdkVersion)
	if err != nil {
		return fmt.Errorf("failed to find SDK version '%s': %w", o.flagSdkVersion, err)
	}
	if sdkVersionInfo == nil {
		return fmt.Errorf("SDK version '%s' not found in Metaplay portal", o.flagSdkVersion)
	}

	// Ensure that the SDK version is valid and supported.
	_, err = parseAndValidateSdkVersion(sdkVersionInfo.Version)
	if err != nil {
		return err
	}

	// Ensure the SDK version is downloadable.
	if sdkVersionInfo.StoragePath == nil {
		return fmt.Errorf("selected SDK version does not have a downloadable file")
	}

	// Prevent accidental overwrite when MetaplaySDK already exists in target
	if _, err := os.Stat(targetSdkDirAbs); err == nil {
		return fmt.Errorf("MetaplaySDK/ directory already exists at %s", targetSdkDirAbs)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Download Metaplay SDK"))
	log.Info().Msg("")
	log.Info().Msgf("SDK version:                 %s", styles.RenderTechnical(sdkVersionInfo.Version))
	log.Info().Msgf("MetaplaySDK directory:       %s", styles.RenderTechnical(targetSdkDirAbs))
	log.Info().Msgf("Extraction parent directory: %s", styles.RenderTechnical(parentDir))
	log.Info().Msg("")

	// Download and extract the SDK.
	// Pass the parent directory since the extractor creates MetaplaySDK/ within it
	if _, err := downloadAndExtractSdk(tokenSet, parentDir, sdkVersionInfo); err != nil {
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ Metaplay SDK downloaded and extracted successfully!"))
	return nil
}
