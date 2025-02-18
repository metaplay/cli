/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package version

import (
	"context"
	"fmt"
	"strings"

	"github.com/creativeprojects/go-selfupdate"
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

func CheckVersion(stderrLogger *zerolog.Logger) {
	if IsDevBuild() {
		log.Debug().Msgf("Bypassing self-updater version checks for development builds (version is '%s')", AppVersion)
		return
	}

	log.Debug().Msgf("Checking for new CLI version (current: v%s)", AppVersion)

	// Check for new releases using go-selfupdate.
	// Errors are only logged, not fatal, in order to allow the command to run even if the version check fails.
	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{
		APIToken: "", // Public repo doesn't need auth
	})
	if err != nil {
		log.Debug().Msg("Error: Failed to initialize the Metaplay CLI self-updater source")
		return
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source: source,
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

	if found && latest.GreaterThan(AppVersion) {
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
}
