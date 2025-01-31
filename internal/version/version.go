package version

import (
	"context"
	"github.com/creativeprojects/go-selfupdate"
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
		stderrLogger.Info().Msg("*******************************************************************************")
		stderrLogger.Info().Msgf("Metaplay CLI v%s is available! Your currently installed version is v%s.", latest.Version(), AppVersion)
		stderrLogger.Info().Msg("Run 'metaplay update' to upgrade.")
		stderrLogger.Info().Msg("*******************************************************************************")
	}
}
