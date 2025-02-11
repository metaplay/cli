/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/pkg/metaproj"
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
	if strings.HasPrefix(installedVersionStr, "v") {
		installedVersionStr = installedVersionStr[1:]
	}

	// Parse installed Node version
	installedVersion, err := version.NewVersion(installedVersionStr)
	if err != nil {
		return fmt.Errorf("failed to parse Node.js version from '%s': %s", installedVersionStr, err)
	}

	// Fail if version older than recommended.
	if installedVersion.LessThan(recommendedVersion) {
		return fmt.Errorf("Node.js version %s or higher is required, but found %s. Please upgrade Node.js: https://nodejs.org/", recommendedVersion, installedVersionStr)
	}

	// Handle cases where the installed version is more recent than recommended:
	// - If installed major version is more recent, fail.
	// - If installed minor version is more recent, warn.
	// - If installer patch version is more recent, log.
	installedMajorVersion := installedVersion.Segments()[0]
	installedMinorVersion := installedVersion.Segments()[1]
	installedPatchVersion := installedVersion.Segments()[2]
	requiredMajorVersion := recommendedVersion.Segments()[0]
	requiredMinorVersion := recommendedVersion.Segments()[1]
	requiredPatchVersion := recommendedVersion.Segments()[2]
	if installedMajorVersion > requiredMajorVersion {
		return fmt.Errorf("installed Node.js version %s is too recent; version v%d.x.y", recommendedVersion, requiredMajorVersion)
	} else if installedMinorVersion > requiredMinorVersion {
		log.Warn().Msgf("Installed Node.js version %s minor version is more recent than recommended; if you encounter any problems, downgrade to version v%d.%d.x", recommendedVersion, requiredMajorVersion, requiredMinorVersion)
	} else if installedPatchVersion > requiredPatchVersion {
		log.Info().Msgf("Installed Node.js version %s is more recent than recommended version %s", installedVersion, recommendedVersion)
	} else {
		log.Info().Msgf("Installed Node.js version: %s matches the recommended version exactly", installedVersion)
	}

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

	// Fail if version older than recommended.
	if installedVersion.LessThan(recommendedVersion) {
		return fmt.Errorf("pnpm version %s or higher is required, but found %s. Please upgrade pnpm!", recommendedVersion, installedVersion)
	}

	// Handle cases where the installed version is more recent than recommended:
	// - If installed major version is more recent, fail.
	// - If installed minor version is more recent, warn.
	// - If installer patch version is more recent, log.
	installedMajorVersion := installedVersion.Segments()[0]
	installedMinorVersion := installedVersion.Segments()[1]
	installedPatchVersion := installedVersion.Segments()[2]
	requiredMajorVersion := recommendedVersion.Segments()[0]
	requiredMinorVersion := recommendedVersion.Segments()[1]
	requiredPatchVersion := recommendedVersion.Segments()[2]
	if installedMajorVersion > requiredMajorVersion {
		return fmt.Errorf("installed pnpm version %s is too recent; version v%d.x.y", recommendedVersion, requiredMajorVersion)
	} else if installedMinorVersion > requiredMinorVersion {
		log.Warn().Msgf("Installed pnpm version %s minor version is more recent than recommended; if you encounter any problems, downgrade to version v%d.%d.x", recommendedVersion, requiredMajorVersion, requiredMinorVersion)
	} else if installedPatchVersion > requiredPatchVersion {
		log.Info().Msgf("Installed pnpm version %s is more recent than recommended version %s", installedVersion, recommendedVersion)
	} else {
		log.Info().Msgf("Installed pnpm version: %s matches the recommended version exactly", installedVersion)
	}

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

	return nil
}
