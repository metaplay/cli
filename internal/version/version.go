/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/metaplay/cli/internal/envutil"
	"github.com/metaplay/cli/internal/pathutil"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const devBuild = "dev"

var (
	AppVersion = devBuild         // In release builds this will be overwritten via ldflags
	GitCommit  = "unknown-commit" // -"-
)

func IsDevBuild() bool {
	return AppVersion == devBuild
}

// IsPrerelease returns true if the current version is a prerelease build
// (e.g. "0.1.3-dev.5"), but not a local development build ("dev").
func IsPrerelease() bool {
	return strings.Contains(AppVersion, "-dev.")
}

func CheckVersion(ctx context.Context, stderrLogger *zerolog.Logger) {
	if IsDevBuild() {
		log.Debug().Msgf("Skipping version check for development build (version is '%s')", AppVersion)
		return
	}

	log.Debug().Msgf("Checking for new CLI version (current: v%s)", AppVersion)

	// Detect the latest version via the non-throttled github.com endpoints (see detect.go).
	// Errors are only logged, not fatal, so the command still runs if the check fails.
	// The request is bounded by a short timeout so it can't hang the command.
	usePrerelease := IsPrerelease()
	latest, err := DetectLatest(ctx, usePrerelease)
	if err != nil {
		log.Debug().Msgf("Failed to detect the latest Metaplay CLI version: %v", err)
		return
	}

	if !IsNewer(latest, AppVersion) {
		return
	}

	// Auto-update prerelease builds (except in CI).
	if usePrerelease && !envutil.IsCI() {
		stderrLogger.Info().Msgf("Auto-updating CLI from %s to %s...",
			styles.RenderError(AppVersion),
			styles.RenderSuccess(latest),
		)
		exe, err := pathutil.GetExecutablePath()
		if err != nil {
			log.Debug().Msgf("Auto-update failed: could not determine executable path: %v", err)
			return
		}
		// Bound the background download: it runs on every command for prerelease builds, so a
		// stalled connection must not hang the command indefinitely. Ctrl+C still interrupts via
		// the command context; the timeout is generous enough for a large archive on a slow link.
		dlCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if err := DownloadAndApply(dlCtx, latest, exe); err != nil {
			log.Debug().Msgf("Auto-update failed: %v", err)
			return
		}
		stderrLogger.Info().Msgf(styles.RenderSuccess("Updated CLI to %s.")+" Note: this command is still running with the previous version.", latest)
		return
	}

	// GA builds: show update banner.
	for _, line := range renderUpdateBanner(AppVersion, latest) {
		stderrLogger.Info().Msg(line)
	}
}

// renderUpdateBanner builds the bordered update notification box.
// currentVersion and latestVersion are plain version strings (no ANSI).
func renderUpdateBanner(currentVersion, latestVersion string) []string {
	updateLine := fmt.Sprintf("Update available! %s → %s", currentVersion, latestVersion)
	commandLine := "To update, run: metaplay update cli"

	// Inner width (in runes) = longest content line + 4 (2 chars padding each side).
	updateWidth := utf8.RuneCountInString(updateLine)
	commandWidth := utf8.RuneCountInString(commandLine)
	innerWidth := max(updateWidth, commandWidth) + 4

	pad := func(visibleWidth int) string {
		return strings.Repeat(" ", innerWidth-2-visibleWidth)
	}

	return []string{
		fmt.Sprintf("╭%s╮", strings.Repeat("─", innerWidth)),
		fmt.Sprintf("│%s│", strings.Repeat(" ", innerWidth)),
		fmt.Sprintf("│  %s %s → %s%s│",
			"Update available!",
			styles.RenderError(currentVersion),
			styles.RenderSuccess(latestVersion),
			pad(updateWidth),
		),
		fmt.Sprintf("│  %s%s│",
			styles.RenderPrompt("To update, run: metaplay update cli"),
			pad(commandWidth),
		),
		fmt.Sprintf("│%s│", strings.Repeat(" ", innerWidth)),
		fmt.Sprintf("╰%s╯", strings.Repeat("─", innerWidth)),
	}
}
