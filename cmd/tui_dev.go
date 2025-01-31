/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"time"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type tuiDevOpts struct {
	flagProjectID     string
	flagSimulateError bool
}

func init() {
	o := &tuiDevOpts{}

	cmd := &cobra.Command{
		Use:   "tui-dev",
		Short: "Test command for developing TUI components",
		Long: trimIndent(`
			Demonstrates the task runner UI component by running a series of simulated tasks.
			Each task runs for a different duration to show the real-time status updates.

			One task is configured to fail to demonstrate error handling.
		`),
		Run: runCommand(o),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagProjectID, "project-id", "", "The ID for your project, eg, 'gorgeous-bear' (optional)")
	flags.BoolVar(&o.flagSimulateError, "simulate-error", false, "Simulate a task failure")

	cmd.Hidden = true
	rootCmd.AddCommand(cmd)
}

func (o *tuiDevOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil // No validation needed for this test command
}

func (o *tuiDevOpts) Run(cmd *cobra.Command) error {
	// Make sure the user is logged in.
	tokenSet, err := tui.RequireLoggedIn(cmd.Context())
	if err != nil {
		return err
	}

	// Choose target project either with human ID provided as flag or interactively
	// let the user choose from a list of projects fetched from the portal.
	var targetProject *portalapi.PortalProjectInfo
	if o.flagProjectID != "" {
		portal := portalapi.NewClient(tokenSet)
		targetProject, err = portal.FetchProjectInfo(o.flagProjectID)
		if err != nil {
			return err
		}
	} else {
		targetProject, err = tui.ChooseOrgAndProject(tokenSet)
		if err != nil {
			return err
		}
	}

	// Create task runner
	runner := tui.NewTaskRunner(fmt.Sprintf("Integrating Metaplay SDK to project %s", targetProject.HumanID))

	// Add tasks with their functions
	runner.AddTask("Download the Metaplay SDK", func() error {
		log.Debug().Msg("Simulating dependency download")
		time.Sleep(2 * time.Second)
		return nil
	})

	runner.AddTask("Extract contents to MetaplaySDK/", func() error {
		log.Debug().Msg("Simulating project build")
		time.Sleep(3 * time.Second)
		return nil
	})

	runner.AddTask("Initialize game-specific Backend/", func() error {
		log.Debug().Msg("Simulating test execution")
		time.Sleep(1 * time.Second)
		if o.flagSimulateError {
			return &TaskError{message: "failed to initialize SDK due to something"}
		}
		return nil
	})

	runner.AddTask("Add Metaplay client SDK to Unity manifest.json", func() error {
		log.Debug().Msg("Simulating adding SDK reference")
		time.Sleep(1 * time.Second)
		return nil
	})

	runner.AddTask("Clean up installer", func() error {
		log.Debug().Msg("Simulating clean up")
		time.Sleep(1 * time.Second)
		return nil
	})

	// Run tasks sequentially with UI
	if err := runner.Run(); err != nil {
		// log.Error().Msgf("SDK integration failed: %v", err)
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ SDK integrated successfully!"))
	log.Info().Msg("")
	log.Info().Msg("The following changes were made to your project:")
	log.Info().Msgf("- Added shared game logic code at %s", styles.RenderTechnical("UnityClient/Assets/SharedCode/"))
	log.Info().Msgf("- Added sample scene in %s", styles.RenderTechnical("UnityClient/Assets/MetaplayHelloWorld/"))
	log.Info().Msgf("- Added pre-built game config archive to %s", styles.RenderTechnical("UnityClient/Assets/StreamingAssets/"))
	log.Info().Msgf("- Added reference to Metaplay Client SDK in %s", styles.RenderTechnical("UnityClient/Package/manifest.json"))
	return nil
}

// TaskError represents a task execution error
type TaskError struct {
	message string
}

func (e *TaskError) Error() string {
	return e.message
}
