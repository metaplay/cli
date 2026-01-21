/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// Checks if Node.js is installed and verifies the version
func checkNodeVersion(recommendedVersion *version.Version) error {
	// Run the 'node --version' command
	cmd := exec.Command("node", "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return errors.New("Node.js is not installed or not in PATH. Please install Node.js from: https://nodejs.org/")
	}

	// Node.js version output starts with 'v' (e.g., "v22.13.1"), so strip it
	installedVersionStr := strings.TrimSpace(out.String())
	installedVersionStr = strings.TrimPrefix(installedVersionStr, "v")

	// Parse installed Node version
	installedVersion, err := version.NewVersion(installedVersionStr)
	if err != nil {
		return fmt.Errorf("failed to parse Node.js version from '%s': %s", installedVersionStr, err)
	}

	// Fail if version older than recommended.
	if installedVersion.LessThan(recommendedVersion) {
		return fmt.Errorf("Node.js version %s or higher is required, but found %s. Please upgrade Node.js: https://nodejs.org/", recommendedVersion, installedVersionStr)
	}

	// If installed major version is more recent than expected, fail.
	installedMajorVersion := installedVersion.Segments()[0]
	requiredMajorVersion := recommendedVersion.Segments()[0]
	if installedMajorVersion > requiredMajorVersion {
		return fmt.Errorf("detected pnpm version %s is too recent; expecting version v%d.x.y", installedVersion, requiredMajorVersion)
	}

	// Print the info.
	badge := styles.RenderMuted(fmt.Sprintf("[minimum: %s]", recommendedVersion))
	installedMinorVersion := installedVersion.Segments()[1]
	requiredMinorVersion := recommendedVersion.Segments()[1]
	if installedMinorVersion > requiredMinorVersion {
		badge = badge + " " + styles.RenderWarning("[minor version is more recent; downgrade if you encounter any problems]")
	}

	log.Info().Msgf("%s Node.js detected: %s %s", styles.RenderSuccess("✓"), styles.RenderTechnical(installedVersion.String()), badge)

	return nil
}

// Checks if pnpm is installed and verifies the version
func checkPnpmVersion(recommendedVersion *version.Version) error {
	// Run the 'pnpm --version' command
	cmd := exec.Command("pnpm", "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return errors.New("pnpm is not installed or not in PATH. Please install pnpm: https://pnpm.io/installation")
	}

	// Parse pnpm version
	installedVersionStr := strings.TrimSpace(out.String())
	installedVersion, err := version.NewVersion(installedVersionStr)
	if err != nil {
		return fmt.Errorf("failed to parse pnpm version from '%s': %v", installedVersionStr, err)
	}

	// Fail if installed version is older than required.
	if installedVersion.LessThan(recommendedVersion) {
		return fmt.Errorf("pnpm version %s or higher is required, but found %s. Please upgrade pnpm!", recommendedVersion, installedVersion)
	}

	// Grab versions.
	installedMajorVersion := installedVersion.Segments()[0]
	installedMinorVersion := installedVersion.Segments()[1]
	requiredMajorVersion := recommendedVersion.Segments()[0]
	requiredMinorVersion := recommendedVersion.Segments()[1]

	// If installed major version is more recent than expected, fail.
	if installedMajorVersion > requiredMajorVersion {
		return fmt.Errorf("detected pnpm version %s is too recent; expecting version v%d.x.y", installedVersion, requiredMajorVersion)
	}

	// Print the info.
	badge := styles.RenderMuted(fmt.Sprintf("[minimum: %s]", recommendedVersion))
	if installedMinorVersion > requiredMinorVersion {
		badge = badge + " " + styles.RenderWarning("[warning: minor version is more recent; downgrade if you encounter any problems]")
	}

	log.Info().Msgf("%s pnpm detected:    %s %s", styles.RenderSuccess("✓"), styles.RenderTechnical(installedVersion.String()), badge)

	return nil
}

func checkDashboardToolVersions(project *metaproj.MetaplayProject) error {
	// Check for Node installation and minimum required version.
	if err := checkNodeVersion(project.VersionMetadata.RecommendedNodeVersion); err != nil {
		return err
	}

	// Check for pnpm installation and minimum required version.
	if err := checkPnpmVersion(project.VersionMetadata.RecommendedPnpmVersion); err != nil {
		return err
	}

	log.Info().Msg("")

	return nil
}

func cleanTemporaryDashboardFiles(projectRootPath string, dashboardPath string) error {
	log.Info().Msgf("Cleaning up temporary files in %s", projectRootPath)
	// Collect all node_modules folders to delete
	var foldersToDelete []string
	if err := filepath.Walk(projectRootPath, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && info.Name() == "node_modules" {
			foldersToDelete = append(foldersToDelete, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("Failed to collect node_modules folders: %w", err)
	}

	// Delete the collected node_modules folders
	for _, folder := range foldersToDelete {
		log.Info().Msgf("Deleting node_modules folder: %s", folder)
		if err := os.RemoveAll(folder); err != nil {
			// Recursive folders might be deleted already, so just log the error and continue
			log.Warn().Msgf("Failed to delete folder %s: %s", folder, err)
		}
	}

	// Log the number of deleted folders
	if len(foldersToDelete) == 0 {
		log.Info().Msgf("No node_modules folders found in %s", projectRootPath)
	} else {
		log.Info().Msgf("Deleted %d node_modules folders in %s", len(foldersToDelete), projectRootPath)
	}

	// Remove dist/ if it exists.
	distPath := fmt.Sprintf("%s/dist", dashboardPath)
	if _, err := os.Stat(distPath); err == nil {
		log.Info().Msg("Removing existing dist/ directory for a clean install...")
		if err := os.RemoveAll(distPath); err != nil {
			return fmt.Errorf("Failed to remove existing dist/ directory: %s", err)
		}
	}

	return nil
}
