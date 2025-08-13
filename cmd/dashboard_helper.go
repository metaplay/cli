/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
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
