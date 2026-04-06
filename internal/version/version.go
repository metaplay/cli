/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/creativeprojects/go-selfupdate"
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

// IsPrerelease returns true if the current version should use the prerelease
// update channel. This includes both prerelease builds (e.g. "0.1.3-dev.5")
// and local development builds ("dev").
func IsPrerelease() bool {
	return strings.Contains(AppVersion, "-dev.")
}

// PrereleaseOnlySource wraps a Source and filters ListReleases to only return
// prerelease releases. This ensures the updater stays on the prerelease channel
// and won't "upgrade" to a GA release.
type PrereleaseOnlySource struct {
	Inner selfupdate.Source
}

func (s *PrereleaseOnlySource) ListReleases(ctx context.Context, repository selfupdate.Repository) ([]selfupdate.SourceRelease, error) {
	releases, err := s.Inner.ListReleases(ctx, repository)
	if err != nil {
		return nil, err
	}
	filtered := make([]selfupdate.SourceRelease, 0, len(releases))
	for _, r := range releases {
		if r.GetPrerelease() {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

func (s *PrereleaseOnlySource) DownloadReleaseAsset(ctx context.Context, rel *selfupdate.Release, assetID int64) (io.ReadCloser, error) {
	return s.Inner.DownloadReleaseAsset(ctx, rel, assetID)
}

func CheckVersion(stderrLogger *zerolog.Logger) {
	if IsDevBuild() {
		log.Debug().Msgf("Skipping version check for development build (version is '%s')", AppVersion)
		return
	}

	log.Debug().Msgf("Checking for new CLI version (current: v%s)", AppVersion)

	// Check for new releases using go-selfupdate.
	// Errors are only logged, not fatal, in order to allow the command to run even if the version check fails.
	ghSource, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{
		APIToken: "", // Public repo doesn't need auth
	})
	if err != nil {
		log.Debug().Msg("Error: Failed to initialize the Metaplay CLI self-updater source")
		return
	}

	var source selfupdate.Source = ghSource
	usePrerelease := IsPrerelease() || IsDevBuild()
	if usePrerelease {
		source = &PrereleaseOnlySource{Inner: source}
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:     source,
		Prerelease: usePrerelease,
	})
	if err != nil {
		log.Debug().Msg("Error: Failed to initialize the Metaplay CLI self-updater")
		return
	}

	latest, found, err := updater.DetectLatest(context.Background(), selfupdate.ParseSlug("metaplay/cli"))
	if err != nil {
		log.Debug().Msg("Error: Failed to detect the latest Metaplay CLI version")
		return
	}

	if !found {
		return
	}

	if !latest.GreaterThan(AppVersion) {
		return
	}

	// Auto-update prerelease builds (except in CI).
	if usePrerelease && !envutil.IsCI() {
		stderrLogger.Info().Msgf("Auto-updating CLI from %s to %s...",
			styles.RenderError(AppVersion),
			styles.RenderSuccess(latest.Version()),
		)
		exe, err := pathutil.GetExecutablePath()
		if err != nil {
			log.Debug().Msgf("Auto-update failed: could not determine executable path: %v", err)
			return
		}
		if err := updater.UpdateTo(context.Background(), latest, exe); err != nil {
			log.Debug().Msgf("Auto-update failed: %v", err)
			return
		}
		stderrLogger.Info().Msgf(styles.RenderSuccess("Updated CLI to %s.") + " Note: this command is still running with the previous version.", latest.Version())
		return
	}

	// GA builds: show update banner.
	topBorder := "╭──────────────────────────────────────────────────────────────────╮"
	bottomBorder := "╰──────────────────────────────────────────────────────────────────╯"
	emptyLine := "│                                                                  │"

	padding := strings.Repeat(" ", 41-len(AppVersion)-len(latest.Version()))
	updateText := fmt.Sprintf("│  %s %s → %s %s │",
		"Update available!",
		styles.RenderError(AppVersion),
		styles.RenderSuccess(latest.Version()),
		padding,
	)

	commandText := fmt.Sprintf("│  To update, run: %s %s │",
		styles.RenderPrompt("metaplay update cli"),
		strings.Repeat(" ", 27),
	)

	stderrLogger.Info().Msg(topBorder)
	stderrLogger.Info().Msg(emptyLine)
	stderrLogger.Info().Msg(updateText)
	stderrLogger.Info().Msg(commandText)
	stderrLogger.Info().Msg(emptyLine)
	stderrLogger.Info().Msg(bottomBorder)
}
