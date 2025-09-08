/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// \todo Detect whether game server and dashboard are running locally; give good error messages if not

type testSystemOpts struct{}

func init() {
	o := testSystemOpts{}

	cmd := &cobra.Command{
		Use:   "system",
		Short: "Run Playwright.NET system tests",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Run Playwright.NET system tests for the SDK and the project (if present).

			This will run:
			- SDK system tests: Backend/System.Tests
			- Project system tests (if present): Backend/System.Tests

			You must have Playwright.NET installed locally to run these tests.
			Install it with: dotnet tool install --global Microsoft.Playwright.CLI

			Note: The game server and dashboard must be running locally for these tests
			to work!
		`),
		Example: renderExample(`
			# Run Playwright.NET system tests (make sure the server is running locally!)
			metaplay test system
		`),
	}

	testCmd.AddCommand(cmd)
}

func (o *testSystemOpts) Prepare(cmd *cobra.Command, args []string) error { return nil }

func (o *testSystemOpts) Run(cmd *cobra.Command) error {
	// Resolve project
	project, err := resolveProject()
	if err != nil {
		log.Error().Msgf("Failed to find project: %v", err)
		os.Exit(1)
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Run Playwright.NET System Tests"))
	log.Info().Msg("")

	// Check for .NET SDK installation and required version (based on SDK version)
	if err := checkDotnetSdkVersion(project.VersionMetadata.MinDotnetSdkVersion); err != nil {
		return err
	}

	// Ensure Playwright CLI is available.
	if _, err := exec.LookPath("playwright"); err != nil {
		log.Error().Msg(styles.RenderError("Playwright CLI not found on PATH"))
		log.Info().Msg("Install it with:")
		log.Info().Msg(styles.RenderPrompt("dotnet tool install --global Microsoft.Playwright.CLI"))
		return errors.New("playwright CLI not found; please install it and re-run")
	}

	// Resolve SDK System.Tests directory and install browsers
	sdkRootDir := project.GetSdkRootDir()
	sdkSystemTestsDir := filepath.Join(sdkRootDir, "Backend", "System.Tests")

	// Install Playwright browsers.
	log.Info().Msg(styles.RenderBright("🔷 Install Playwright browsers"))
	if err := execChildTask(sdkSystemTestsDir, "playwright", []string{"install"}); err != nil {
		log.Error().Msgf("Playwright install failed in %s: %v", sdkSystemTestsDir, err)
		os.Exit(1)
	}

	// SDK core system tests
	log.Info().Msg("")
	log.Info().Msg(styles.RenderBright("🔷 Run tests in MetaplaySDK/Backend/System.Tests"))
	if err := execChildTask(sdkSystemTestsDir, "dotnet", []string{"test"}); err != nil {
		log.Error().Msgf("SDK system tests failed in %s: %v", sdkSystemTestsDir, err)
		os.Exit(1)
	}

	// Userland system tests (if present)
	log.Info().Msg("")
	userBackendRootDir := project.GetBackendDir()
	userSystemTestsDir := filepath.Join(userBackendRootDir, "System.Tests")
	if st, err := os.Stat(userSystemTestsDir); err == nil && st.IsDir() {
		log.Info().Msg(styles.RenderBright("🔷 Run tests in Backend/System.Tests"))
		if err := execChildTask(userSystemTestsDir, "dotnet", []string{"test"}); err != nil {
			log.Error().Msgf("Project system tests failed in %s: %v", userSystemTestsDir, err)
			os.Exit(1)
		}
	} else {
		log.Info().Msg(styles.RenderMuted("No project-specific system tests project found in Backend/System.Tests (skipping)"))
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Playwright.NET system tests completed successfully"))
	return nil
}
