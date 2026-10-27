/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// initSdkOpts holds options for the 'metaplay init sdk' command.
type initSdkOpts struct {
	flagSdkVersion         string // Metaplay SDK version (e.g., "34.0") or version name.
	flagSdkDirectory       string // Path of the MetaplaySDK directory. Defaults to sdkRootDir in metaplay-project.yaml.
	flagAutoAgreeContracts bool   // Automatically agree to the terms & conditions.
	flagOverwrite          bool   // Remove existing MetaplaySDK/ directory before extracting.
}

func init() {
	o := &initSdkOpts{}

	cmd := &cobra.Command{
		Use:   "sdk",
		Short: "Download the Metaplay SDK into the current project",
		Run:   runCommand(o),
		Long: renderLong(o, `
			Download and extract a Metaplay SDK release into the current project.

			The SDK is extracted to where sdkRootDir in metaplay-project.yaml points. Use --sdk-directory to
			extract it elsewhere. An existing SDK is replaced only after confirmation, or with --overwrite.

			Use this for projects that come without the SDK, such as Metaplay samples and templates. To change
			the SDK version of a project that already has one, use 'metaplay update sdk'.

			You must be logged in to the Metaplay portal (use 'metaplay auth login') and have agreed to the privacy policy
			and terms & conditions in the portal (https://portal.metaplay.dev).
		`),
		Example: renderExample(`
			# Download SDK v34.0 into the current project (creates ./MetaplaySDK/)
			metaplay init sdk --sdk-version=34.0

			# Download SDK v34.0 into a custom directory (creates ../Shared/MetaplaySDK/)
			metaplay init sdk --sdk-version=34.0 --sdk-directory=../Shared/MetaplaySDK
		`),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagSdkVersion, "sdk-version", "", "Metaplay SDK version to download")
	flags.StringVar(&o.flagSdkDirectory, "sdk-directory", "", "Path of the MetaplaySDK directory to create")
	flags.BoolVar(&o.flagAutoAgreeContracts, "auto-agree", false, "Automatically agree to the privacy policy and terms and conditions")
	flags.BoolVar(&o.flagOverwrite, "overwrite", false, "Remove existing MetaplaySDK/ directory and replace with new contents")

	initCmd.AddCommand(cmd)
}

func (o *initSdkOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *initSdkOpts) Run(cmd *cobra.Command) error {
	// Resolve the directory into which the SDK will be extracted.
	// Use --sdk-directory if given, otherwise sdkRootDir from metaplay-project.yaml.
	sdkDirectory := o.flagSdkDirectory
	if sdkDirectory == "" {
		projectDir, err := findProjectDirectory()
		if errors.Is(err, errProjectNotFound) {
			return clierrors.New("--sdk-directory is required outside a Metaplay project").
				WithSuggestion("Run the command in a directory with metaplay-project.yaml, or pass --sdk-directory.")
		}
		if err != nil {
			return err
		}

		projectConfig, err := metaproj.LoadProjectConfigFile(projectDir)
		if err != nil {
			return clierrors.Wrap(err, "Failed to load metaplay-project.yaml")
		}
		sdkDirectory = filepath.Join(projectDir, projectConfig.SdkRootDir)
	}

	abs, err := filepath.Abs(sdkDirectory)
	if err != nil {
		return clierrors.Wrapf(err, "Failed to resolve SDK directory '%s'", sdkDirectory)
	}

	// Assert that the last segment is 'MetaplaySDK'
	if filepath.Base(abs) != "MetaplaySDK" {
		return clierrors.Newf("SDK directory must be named 'MetaplaySDK', got '%s'", sdkDirectory)
	}

	targetSdkDirAbs := abs
	parentDir := filepath.Dir(targetSdkDirAbs)

	// Ensure the user is logged in (default auth provider).
	authProvider, err := auth.NewDefaultAuthProvider()
	if err != nil {
		return err
	}
	tokenSet, err := tui.RequireLoggedIn(cmd.Context(), authProvider)
	if err != nil {
		return err
	}

	// Agree to contracts if needed (required for SDK download)
	portalClient := portalapi.NewClient(tokenSet)
	if err := ensureSdkDownloadContractsAccepted(cmd.Context(), portalClient, o.flagAutoAgreeContracts); err != nil {
		return err
	}

	// Resolve SDK version info (must exist and be downloadable).
	// Without --sdk-version, offer the latest version in interactive mode.
	var sdkVersionInfo *portalapi.SdkVersionInfo
	if o.flagSdkVersion == "" {
		if !tui.IsInteractiveMode() {
			return clierrors.NewUsageError("--sdk-version is required").
				WithSuggestion("Check available versions with 'metaplay get sdk-versions'")
		}

		sdkVersionInfo, err = portalClient.GetLatestSdkVersionInfo()
		if err != nil {
			return clierrors.Wrap(err, "Failed to get the latest SDK version")
		}

		confirmed, err := tui.DoConfirmQuestion(cmd.Context(), fmt.Sprintf("Use latest SDK (%s)?", sdkVersionInfo.Version))
		if err != nil {
			return err
		}
		if !confirmed {
			return clierrors.New("No SDK version selected").
				WithSuggestion("Pass the version with --sdk-version. See available versions with 'metaplay get sdk-versions'.")
		}
	} else {
		sdkVersionInfo, err = portalClient.FindSdkVersionByVersionOrName(o.flagSdkVersion)
		if err != nil {
			return clierrors.Wrapf(err, "Failed to find SDK version '%s'", o.flagSdkVersion)
		}
		if sdkVersionInfo == nil {
			return clierrors.Newf("SDK version '%s' not found", o.flagSdkVersion).
				WithSuggestion("Check available versions with 'metaplay get sdk-versions'")
		}
	}

	// Ensure that the SDK version is valid and supported.
	err = parseAndValidateSdkVersion(sdkVersionInfo.Version)
	if err != nil {
		return err
	}

	// Ensure the SDK version is downloadable.
	if sdkVersionInfo.StoragePath == nil {
		return clierrors.Newf("SDK version %s has no downloadable file", sdkVersionInfo.Version)
	}

	// Handle existing MetaplaySDK directory
	if _, err := os.Stat(targetSdkDirAbs); err == nil {
		// Try to resolve the existing SDK version (fail gracefully if not found)
		existingVersionStr := "unknown"
		existingMetadata, err := metaproj.LoadSdkVersionMetadata(targetSdkDirAbs)
		if err == nil && existingMetadata != nil && existingMetadata.SdkVersion != nil {
			existingVersionStr = existingMetadata.SdkVersion.String()
		}

		// If --overwrite flag wasn't given, ask for confirmation in interactive mode.
		if !o.flagOverwrite {
			// Ask the user for confirmation in interactive mode
			if !tui.IsInteractiveMode() {
				return clierrors.Newf("MetaplaySDK directory already exists at %s", targetSdkDirAbs).
					WithSuggestion("Use --overwrite to replace it")
			}

			// Display information about the existing SDK and the requested version
			log.Info().Msg("")
			log.Info().Msg(styles.RenderWarning("⚠️  MetaplaySDK directory already exists"))
			log.Info().Msg("")
			log.Info().Msgf("  Location:         %s", styles.RenderTechnical(targetSdkDirAbs))
			log.Info().Msgf("  Existing version: %s", styles.RenderTechnical(existingVersionStr))
			log.Info().Msgf("  New version:      %s", styles.RenderTechnical(sdkVersionInfo.Version))
			log.Info().Msg("")

			confirmed, err := tui.DoConfirmQuestion(cmd.Context(), "Do you want to overwrite the existing SDK?")
			if err != nil {
				return err
			}
			if !confirmed {
				log.Info().Msg(styles.RenderError("❌ Operation canceled"))
				return nil
			}
		}

		// Remove existing directory
		log.Info().Msgf("Removing existing MetaplaySDK directory: %s", targetSdkDirAbs)
		if err := os.RemoveAll(targetSdkDirAbs); err != nil {
			return clierrors.Wrap(err, "Failed to remove the existing MetaplaySDK directory")
		}
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return clierrors.Wrapf(err, "Failed to create directory %s", parentDir)
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
	if err := downloadAndExtractSdk(tokenSet, parentDir, sdkVersionInfo, nil); err != nil {
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ Metaplay SDK downloaded and extracted successfully!"))
	return nil
}
