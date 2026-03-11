/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type testDotnetUnitOpts struct{}

// \todo Add support for customizing the unit test projects to run (via metaplay-project.yaml?)

func init() {
	o := testDotnetUnitOpts{}

	cmd := &cobra.Command{
		Use:   "dotnet-unit",
		Short: "[preview] Run .NET unit tests for the project",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is still in preview and subject to change.

			Run the available .NET unit tests in the project locally.

			The following .NET unit test projects are run (if present):
			- Backend/SharedCode.Tests
			- Backend/Server.Tests

			This includes running the Metaplay SDK unit test projects:
			- Backend/Cloud.Tests
			- Backend/Cloud.Serialization.Compilation.Tests
			- Backend/Server.Tests
		`),
		Example: renderExample(`
			# Run .NET unit tests
			metaplay test dotnet-unit
		`),
	}

	cmd.Hidden = true // Not yet ready for prime-time, likely to change

	testCmd.AddCommand(cmd)
}

func (o *testDotnetUnitOpts) Prepare(cmd *cobra.Command, args []string) error { return nil }

func (o *testDotnetUnitOpts) Run(cmd *cobra.Command) error {
	// Resolve project
	project, err := resolveProject()
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Run .NET Unit Tests"))
	log.Info().Msg("")

	// Check for .NET SDK installation and required version (based on SDK version)
	if err := checkDotnetSdkVersion(project.VersionMetadata.MinDotnetSdkVersion); err != nil {
		return err
	}

	// Construct absolute paths to the test projects under the SDK root
	sdkRoot := project.GetSdkRootDir()
	testProjects := []string{
		filepath.Join(sdkRoot, "Backend", "Cloud.Tests"),
		filepath.Join(sdkRoot, "Backend", "Cloud.Serialization.Compilation.Tests"),
		filepath.Join(sdkRoot, "Backend", "Server.Tests"),
	}

	// Execute SDK tests one project at a time for clearer output
	for _, projPath := range testProjects {
		log.Info().Msg(styles.RenderBright(fmt.Sprintf("🔷 Run tests in %s", filepath.Base(projPath))))
		if err := execChildTask(projPath, "dotnet", []string{"test"}); err != nil {
			return clierrors.Wrapf(err, "Unit tests failed in %s", projPath)
		}
	}

	// Now run game-specific/userland tests if these projects exist under the project's backend directory
	backendRoot := project.GetBackendDir()
	userTestProjects := []string{
		filepath.Join(backendRoot, "SharedCode.Tests"),
		filepath.Join(backendRoot, "Server.Tests"),
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Run Project Unit Tests (if present)"))

	for _, projPath := range userTestProjects {
		if st, err := os.Stat(projPath); err == nil && st.IsDir() {
			log.Info().Msg("")
			log.Info().Msg(styles.RenderBright(fmt.Sprintf("🔷 Run tests in %s", filepath.Base(projPath))))
			if err := execChildTask(projPath, "dotnet", []string{"test"}); err != nil {
				return clierrors.Wrapf(err, "Unit tests failed in %s", projPath)
			}
		}
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ All .NET unit tests passed"))
	return nil
}
