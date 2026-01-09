/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type updateSdkOpts struct {
	flagToVersion          string // Target SDK version (non-interactive mode)
	flagAutoAgreeContracts bool   // Automatically agree to contracts
	flagYes                bool   // Skip confirmation prompts
}

func init() {
	o := &updateSdkOpts{}

	cmd := &cobra.Command{
		Use:   "sdk",
		Short: "Update the Metaplay SDK to a newer version",
		Run:   runCommand(o),
		Long: renderLong(o, `
			Update the Metaplay SDK in your project to a newer version.

			This command reads the current SDK version from MetaplaySDK/version.yaml,
			fetches available versions from the Metaplay portal, and offers update options:
			- Latest minor version within your current major release (e.g., 34.1 -> 34.3)
			- Latest version in the next major release (e.g., 34.x -> 35.2)

			Note: If local modifications to SDK files are detected, a patch file
			(metaplay-sdk-modifications.patch) will be extracted before updating. You can
			re-apply the changes with 'patch -p1' or 'git apply --reject'. Some hunks may
			fail if there are conflicts with the new SDK version and will require manual
			resolution.

			You may also use your own preferred way to preserve the changes, in which case,
			you can ignore the generated patch file.

			You must be logged in to the Metaplay portal (use 'metaplay auth login').
		`),
		Example: renderExample(`
			# Interactive update - choose from available versions
			metaplay update sdk

			# Update to a specific version
			metaplay update sdk --to-version=35.2

			# Update to latest in a major version (e.g., latest 35.x)
			metaplay update sdk --to-version=35

			# Non-interactive update for CI/automation
			metaplay update sdk --to-version=35 --auto-agree --yes
		`),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagToVersion, "to-version", "", "Target SDK version to update to (required in non-interactive mode)")
	flags.BoolVar(&o.flagAutoAgreeContracts, "auto-agree", false, "Automatically agree to privacy policy and terms & conditions")
	flags.BoolVar(&o.flagYes, "yes", false, "Skip confirmation prompts")

	updateCmd.AddCommand(cmd)
}

func (o *updateSdkOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate non-interactive mode requirements
	if !tui.IsInteractiveMode() && o.flagToVersion == "" {
		return fmt.Errorf("in non-interactive mode, --to-version is required")
	}
	return nil
}

// formatMajorMinor formats a version as "major.minor" (without patch).
func formatMajorMinor(v *version.Version) string {
	segments := v.Segments()
	if len(segments) >= 2 {
		return fmt.Sprintf("%d.%d", segments[0], segments[1])
	}
	return v.String()
}

func (o *updateSdkOpts) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Find and load project config
	projectDir, err := findProjectDirectory()
	if err != nil {
		return err
	}

	projectConfig, err := metaproj.LoadProjectConfigFile(projectDir)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	// Resolve SDK root directory
	sdkRootDir := filepath.Join(projectDir, projectConfig.SdkRootDir)
	sdkRootDirAbs, err := filepath.Abs(sdkRootDir)
	if err != nil {
		return fmt.Errorf("failed to resolve SDK path: %w", err)
	}

	// Check SDK directory exists
	if _, err := os.Stat(sdkRootDirAbs); os.IsNotExist(err) {
		return fmt.Errorf("MetaplaySDK directory not found at %s", sdkRootDirAbs)
	}

	// Load current SDK version
	versionMetadata, err := metaproj.LoadSdkVersionMetadata(sdkRootDirAbs)
	if err != nil {
		return fmt.Errorf("failed to read current SDK version: %w\n\nThis SDK version is not supported by the update command. Please update manually", err)
	}
	currentVersion := versionMetadata.SdkVersion

	// Display header
	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Update Metaplay SDK"))
	log.Info().Msg("")

	// Authenticate
	authProvider := auth.NewMetaplayAuthProvider()
	tokenSet, err := tui.RequireLoggedIn(ctx, authProvider)
	if err != nil {
		return err
	}

	// Detect local SDK modifications
	portalClient := portalapi.NewClient(tokenSet)
	currentVersionInfo, err := portalClient.FindSdkVersionByVersionOrName(formatMajorMinor(currentVersion))
	if err != nil {
		log.Debug().Msgf("Could not look up current SDK version in portal: %v", err)
	}

	var modifications []ModifiedFile
	var patchContent string
	var modificationCheckDone bool
	if currentVersionInfo != nil {
		log.Info().Msgf("Preparing update...")
		sdkZipPath, err := downloadSdkZipOnly(tokenSet, currentVersionInfo.ID)
		if err != nil {
			log.Debug().Msgf("Could not download current SDK for comparison: %v", err)
		} else {
			defer os.Remove(sdkZipPath)
			result, err := DetectSdkModificationsWithPatch(sdkRootDirAbs, sdkZipPath)
			if err != nil {
				log.Debug().Msgf("Could not check for SDK modifications: %v", err)
			} else {
				modifications = result.Modifications
				patchContent = result.PatchContent
				modificationCheckDone = true
			}
		}
	}

	// Display current state
	log.Info().Msg("")
	log.Info().Msgf("Current SDK version:  %s", styles.RenderTechnical(formatMajorMinor(currentVersion)))
	log.Info().Msgf("SDK location:         %s", styles.RenderTechnical(sdkRootDirAbs))
	if modificationCheckDone {
		if len(modifications) > 0 {
			log.Info().Msgf("Modifications to SDK: %s", styles.RenderWarning(fmt.Sprintf("%d file(s)", len(modifications))))
		} else {
			log.Info().Msgf("Modifications to SDK: %s", styles.RenderMuted("no changes detected"))
		}
	}
	log.Info().Msg("")

	// Show modified files and warn user before proceeding
	var patchPath string
	if len(modifications) > 0 {
		patchPath = filepath.Join(projectDir, "metaplay-sdk-modifications.patch")
		log.Info().Msg(styles.RenderTitle("SDK Modifications Detected"))
		log.Info().Msg("")
		log.Info().Msgf("The following SDK changes were detected and will be saved to a patch file: %s.", styles.RenderTechnical(patchPath))
		log.Info().Msg("")
		printModifiedFilesList(modifications, 20)
		log.Info().Msg("")

		// Warn about binary files that cannot be included in the patch
		binaryCount := countBinaryFiles(modifications)
		if binaryCount > 0 {
			log.Info().Msg(styles.RenderWarning(fmt.Sprintf("WARNING: %d binary file(s) cannot be included in the patch and WILL BE LOST!", binaryCount)))
			log.Info().Msg("         You must manually back up and restore these files.")
			log.Info().Msg("")
		}

		log.Info().Msgf("After the update, you can re-apply the changes from the SDK directory with either:")
		log.Info().Msgf("  %s", styles.RenderPrompt("patch -p1 < "+patchPath))
		log.Info().Msg("or")
		log.Info().Msgf("  %s", styles.RenderPrompt("git apply --reject "+patchPath))
		log.Info().Msg("")
		log.Info().Msg("Tip: Use 'patch -p1 --dry-run < <patch>' first to preview what will be applied.")
		log.Info().Msg("")
		log.Info().Msg("Note: Some hunks may fail if there are conflicting changes in the new SDK.")
		log.Info().Msg("      Failed hunks are saved to .rej files for manual resolution.")
		log.Info().Msg("")
		log.Info().Msg(styles.RenderWarning("If you encounter any issues with handling the patch, please report them at https://github.com/metaplay/cli/issues"))
		log.Info().Msg("")

		// Ask for confirmation
		if tui.IsInteractiveMode() {
			confirmed, err := tui.DoConfirmQuestion(ctx, "Continue with update?")
			if err != nil {
				return err
			}
			if !confirmed {
				log.Info().Msg(styles.RenderMuted("Update canceled."))
				return nil
			}
		} else {
			if !o.flagYes {
				return fmt.Errorf("local SDK modifications detected; use --yes to proceed anyway")
			}
			log.Warn().Msg("Proceeding despite modifications (--yes specified)")
		}

		// Save patch file
		if patchContent != "" {
			if err := os.WriteFile(patchPath, []byte(patchContent), 0644); err != nil {
				log.Warn().Msgf("Could not save patch file: %v", err)
				patchPath = "" // Clear path so we don't show restore instructions
			}
		}
	}

	// Fetch available versions
	log.Info().Msg("Checking for available updates...")
	allVersions, err := portalClient.GetSdkVersions()
	if err != nil {
		return fmt.Errorf("failed to fetch SDK versions: %w", err)
	}

	// Filter to downloadable versions only
	var versions []portalapi.SdkVersionInfo
	for _, v := range allVersions {
		if v.StoragePath != nil {
			versions = append(versions, v)
		}
	}

	// Select target version
	var targetVersion *portalapi.SdkVersionInfo
	if o.flagToVersion != "" {
		// Non-interactive: use specified version
		targetVersion, err = resolveTargetVersion(o.flagToVersion, versions, currentVersion)
		if err != nil {
			return err
		}
	} else {
		// Interactive: find update options and let user choose
		targetVersion, err = selectVersionInteractively(versions, currentVersion)
		if err != nil {
			return err
		}
		if targetVersion == nil {
			// No updates available, message already printed
			return nil
		}
	}

	// Confirm update (when no modifications were detected)
	if len(modifications) == 0 && !o.flagYes {
		if !tui.IsInteractiveMode() {
			return fmt.Errorf("confirmation required; use --yes to skip")
		}

		log.Info().Msg("")
		confirmed, err := tui.DoConfirmQuestion(ctx, "Proceed with SDK update?")
		if err != nil {
			return err
		}
		if !confirmed {
			log.Info().Msg(styles.RenderMuted("Update canceled."))
			return nil
		}
	}

	// Ensure contracts accepted
	if err := ensureSdkDownloadContractsAccepted(ctx, portalClient, o.flagAutoAgreeContracts); err != nil {
		return err
	}

	// Validate the target SDK version is supported
	if _, err := parseAndValidateSdkVersion(targetVersion.Version); err != nil {
		return err
	}

	// Apply the update
	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle(fmt.Sprintf("Updating SDK to %s", styles.RenderTechnical(targetVersion.Version))))
	log.Info().Msg("")

	// Remove existing SDK directory (with retries for transient file locks)
	log.Info().Msgf("  Removing existing SDK at %s...", styles.RenderTechnical(sdkRootDirAbs))
	if err := removeDirectoryWithRetry(sdkRootDirAbs, 3, 2*time.Second); err != nil {
		return fmt.Errorf("failed to remove existing SDK directory: %w\n\nThis can happen if files are in use by another process (e.g., Unity, IDE, dashboard dev server). Close any applications using SDK files and try again", err)
	}

	// Download and extract new SDK
	log.Info().Msgf("  Downloading and extracting SDK %s...", styles.RenderTechnical(targetVersion.Version))
	parentDir := filepath.Dir(sdkRootDirAbs)
	if _, err := downloadAndExtractSdk(tokenSet, parentDir, targetVersion); err != nil {
		return fmt.Errorf("failed to download and extract SDK: %w", err)
	}

	// Success message and release notes
	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess(fmt.Sprintf("✅ SDK updated successfully to version %s!", targetVersion.Version)))

	// Check if this was a major version update
	targetParsed, _ := version.NewVersion(targetVersion.Version)
	isMajorUpdate := targetParsed != nil && targetParsed.Segments()[0] > currentVersion.Segments()[0]

	hasReleaseNotes := targetVersion.ReleaseNotesURL != nil && *targetVersion.ReleaseNotesURL != ""
	releaseNotesURL := "https://docs.metaplay.io/miscellaneous/sdk-updates/release-notes/"
	if hasReleaseNotes {
		releaseNotesURL = *targetVersion.ReleaseNotesURL
	}

	log.Info().Msg("")
	log.Info().Msgf("📖 Release notes: %s", releaseNotesURL)

	if isMajorUpdate {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderPrompt("This is a major release update. Follow the migration guide in the release notes!"))
	}

	return nil
}

// resolveTargetVersion resolves the --to-version flag to a specific SDK version.
// Supports both exact versions (e.g., "35.2") and major-only (e.g., "35" -> latest 35.x).
func resolveTargetVersion(toVersion string, versions []portalapi.SdkVersionInfo, currentVersion *version.Version) (*portalapi.SdkVersionInfo, error) {
	// Check if it's a major-only version (all digits, no dots)
	isMajorOnly := true
	for _, c := range toVersion {
		if c < '0' || c > '9' {
			isMajorOnly = false
			break
		}
	}

	if isMajorOnly {
		// Find latest version for this major
		targetVersion := findLatestForMajor(versions, toVersion)
		if targetVersion == nil {
			return nil, fmt.Errorf("no SDK version found for major version %s", toVersion)
		}

		// Validate it's newer than current
		targetParsed, _ := version.NewVersion(targetVersion.Version)
		if !targetParsed.GreaterThan(currentVersion) {
			return nil, fmt.Errorf("target version %s is not newer than current version %s", targetVersion.Version, currentVersion)
		}

		log.Info().Msgf("Resolved version %s to %s", styles.RenderTechnical(toVersion), styles.RenderTechnical(targetVersion.Version))
		return targetVersion, nil
	}

	// Exact version match
	for i := range versions {
		if versions[i].Version == toVersion {
			targetParsed, _ := version.NewVersion(versions[i].Version)
			if !targetParsed.GreaterThan(currentVersion) {
				return nil, fmt.Errorf("target version %s is not newer than current version %s", versions[i].Version, currentVersion)
			}
			return &versions[i], nil
		}
	}

	return nil, fmt.Errorf("SDK version '%s' not found", toVersion)
}

// sdkVersionOption represents a selectable SDK update option.
type sdkVersionOption struct {
	version     *portalapi.SdkVersionInfo
	parsed      *version.Version
	label       string
	description string
}

// selectVersionInteractively finds available update options and lets the user choose.
func selectVersionInteractively(versions []portalapi.SdkVersionInfo, currentVersion *version.Version) (*portalapi.SdkVersionInfo, error) {
	currentMajor := currentVersion.Segments()[0]
	nextMajor := currentMajor + 1

	// Find the recommended versions (latest in current major, latest in next major)
	latestInCurrentMajor := findLatestMinorUpdate(versions, currentMajor, currentVersion)
	latestInNextMajor := findLatestInMajor(versions, nextMajor)

	// Build list of selectable options (current major + next major only)
	var options []sdkVersionOption
	var futureVersions []portalapi.SdkVersionInfo

	for i := range versions {
		v := &versions[i]
		parsed, err := version.NewVersion(v.Version)
		if err != nil {
			continue
		}

		vMajor := parsed.Segments()[0]

		// Skip versions not newer than current
		if !parsed.GreaterThan(currentVersion) {
			continue
		}

		// Categorize by major version
		if vMajor == currentMajor || vMajor == nextMajor {
			// Selectable: current major or next major
			desc := ""
			if latestInCurrentMajor != nil && v.ID == latestInCurrentMajor.ID {
				desc = "Recommended minor update"
			} else if latestInNextMajor != nil && v.ID == latestInNextMajor.ID {
				desc = "Recommended major update"
			}

			options = append(options, sdkVersionOption{
				version:     v,
				parsed:      parsed,
				label:       v.Version,
				description: desc,
			})
		} else if vMajor > nextMajor {
			// Future: requires skipping a major
			futureVersions = append(futureVersions, *v)
		}
	}

	// Sort options by version descending (newest first)
	sortVersionOptions(options)

	// Check if any updates available
	if len(options) == 0 {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderSuccess(fmt.Sprintf("✅ Your SDK is already up to date (version %s)", formatMajorMinor(currentVersion))))
		return nil, nil
	}

	// Show future versions note if any exist (before the interactive selection)
	if len(futureVersions) > 0 {
		// Sort future versions descending and get unique major versions
		sortVersionInfos(futureVersions)
		futureMajors := getUniqueMajorVersions(futureVersions)

		log.Info().Msg("")
		if len(futureMajors) == 1 {
			log.Info().Msgf(styles.RenderMuted("Note: Release %d is available but requires updating to Release %d first."), futureMajors[0], nextMajor)
		} else {
			log.Info().Msgf(styles.RenderMuted("Note: Releases %s are available but require updating to Release %d first."), formatMajorList(futureMajors), nextMajor)
		}
	}

	// Let user choose using arrow key navigation
	selected, err := tui.ChooseFromListDialog(
		"Choose SDK Version",
		options,
		func(opt *sdkVersionOption) (string, string) {
			return opt.label, opt.description
		},
	)
	if err != nil {
		return nil, err
	}

	log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), selected.version.Version)

	return selected.version, nil
}

// getUniqueMajorVersions returns a sorted slice of unique major version numbers.
func getUniqueMajorVersions(versions []portalapi.SdkVersionInfo) []int {
	seen := make(map[int]bool)
	var majors []int

	for _, v := range versions {
		parsed, err := version.NewVersion(v.Version)
		if err != nil {
			continue
		}
		major := parsed.Segments()[0]
		if !seen[major] {
			seen[major] = true
			majors = append(majors, major)
		}
	}

	// Sort ascending
	for i := 0; i < len(majors)-1; i++ {
		for j := i + 1; j < len(majors); j++ {
			if majors[j] < majors[i] {
				majors[i], majors[j] = majors[j], majors[i]
			}
		}
	}

	return majors
}

// formatMajorList formats a list of major versions as "36, 37, 38".
func formatMajorList(majors []int) string {
	strs := make([]string, len(majors))
	for i, m := range majors {
		strs[i] = fmt.Sprintf("%d", m)
	}
	return fmt.Sprintf("%s", joinWithCommaAnd(strs))
}

// joinWithCommaAnd joins strings with commas and "and" for the last item.
func joinWithCommaAnd(items []string) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 {
		return items[0]
	}
	if len(items) == 2 {
		return items[0] + " and " + items[1]
	}
	result := ""
	for i, item := range items {
		if i == len(items)-1 {
			result += "and " + item
		} else {
			result += item + ", "
		}
	}
	return result
}

// sortVersionOptions sorts SDK version options by version descending (newest first).
func sortVersionOptions(options []sdkVersionOption) {
	for i := 0; i < len(options)-1; i++ {
		for j := i + 1; j < len(options); j++ {
			if options[j].parsed.GreaterThan(options[i].parsed) {
				options[i], options[j] = options[j], options[i]
			}
		}
	}
}

// sortVersionInfos sorts SDK version infos by version descending (newest first).
func sortVersionInfos(versions []portalapi.SdkVersionInfo) {
	for i := 0; i < len(versions)-1; i++ {
		for j := i + 1; j < len(versions); j++ {
			vi, _ := version.NewVersion(versions[i].Version)
			vj, _ := version.NewVersion(versions[j].Version)
			if vi != nil && vj != nil && vj.GreaterThan(vi) {
				versions[i], versions[j] = versions[j], versions[i]
			}
		}
	}
}

// findLatestInMajor finds the latest version for a specific major version.
func findLatestInMajor(versions []portalapi.SdkVersionInfo, major int) *portalapi.SdkVersionInfo {
	var best *portalapi.SdkVersionInfo
	var bestParsed *version.Version

	for i := range versions {
		v := &versions[i]
		parsed, err := version.NewVersion(v.Version)
		if err != nil {
			continue
		}

		if parsed.Segments()[0] != major {
			continue
		}

		if best == nil || parsed.GreaterThan(bestParsed) {
			best = v
			bestParsed = parsed
		}
	}

	return best
}

// findLatestMinorUpdate finds the latest version within the same major version
// that is greater than the current version.
func findLatestMinorUpdate(versions []portalapi.SdkVersionInfo, currentMajor int, currentVersion *version.Version) *portalapi.SdkVersionInfo {
	var best *portalapi.SdkVersionInfo
	var bestParsed *version.Version

	for i := range versions {
		v := &versions[i]
		parsed, err := version.NewVersion(v.Version)
		if err != nil {
			continue
		}

		// Must be same major version
		if parsed.Segments()[0] != currentMajor {
			continue
		}

		// Must be greater than current
		if !parsed.GreaterThan(currentVersion) {
			continue
		}

		// Track the best (highest) version
		if best == nil || parsed.GreaterThan(bestParsed) {
			best = v
			bestParsed = parsed
		}
	}

	return best
}

// findLatestMajorUpdate finds the latest version in any major version higher than current.
func findLatestMajorUpdate(versions []portalapi.SdkVersionInfo, currentMajor int) *portalapi.SdkVersionInfo {
	var best *portalapi.SdkVersionInfo
	var bestParsed *version.Version

	for i := range versions {
		v := &versions[i]
		parsed, err := version.NewVersion(v.Version)
		if err != nil {
			continue
		}

		// Must be a higher major version
		if parsed.Segments()[0] <= currentMajor {
			continue
		}

		// Track the best (highest) version
		if best == nil || parsed.GreaterThan(bestParsed) {
			best = v
			bestParsed = parsed
		}
	}

	return best
}

// findLatestForMajor finds the latest version for a specific major version number.
func findLatestForMajor(versions []portalapi.SdkVersionInfo, majorStr string) *portalapi.SdkVersionInfo {
	var targetMajor int
	fmt.Sscanf(majorStr, "%d", &targetMajor)

	var best *portalapi.SdkVersionInfo
	var bestParsed *version.Version

	for i := range versions {
		v := &versions[i]
		parsed, err := version.NewVersion(v.Version)
		if err != nil {
			continue
		}

		if parsed.Segments()[0] != targetMajor {
			continue
		}

		if best == nil || parsed.GreaterThan(bestParsed) {
			best = v
			bestParsed = parsed
		}
	}

	return best
}

// removeDirectoryWithRetry attempts to remove a directory, retrying on failure.
// This helps handle transient file locks on Windows.
func removeDirectoryWithRetry(path string, maxRetries int, delay time.Duration) error {
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err = os.RemoveAll(path)
		if err == nil {
			return nil
		}

		// If this was the last attempt, don't wait
		if attempt == maxRetries {
			break
		}

		// Wait before retrying
		log.Debug().Msgf("Failed to remove directory (attempt %d/%d), retrying in %v: %v", attempt+1, maxRetries+1, delay, err)
		time.Sleep(delay)
	}
	return err
}
