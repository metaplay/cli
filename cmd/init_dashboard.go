/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type initDashboardOpts struct {
}

func init() {
	o := initDashboardOpts{}

	cmd := &cobra.Command{
		Use:   "dashboard [flags]",
		Short: "Initializes custom LiveOps Dashboard for the project",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Setup the development environment for a custom LiveOps Dashboard in your project.

			This command does the following:
			1. Populate a fresh LiveOps Dashboard Node.js project into Backend/Dashboard
			2. Initialize the following in your project:
			  - pnpm-workspace.yaml (pnpm workspace configuration file)
			  - Backend/dashboard.code-workspace (Visual Studio Code workspace)
			3. Update metaplay-project.yaml to refer to your custom dashboard.
			4. Generate the pnpm-lock.yaml file using 'pnpm install'.

			Related commands:
			- 'metaplay build dashboard' to build the dashboard locally.
			- 'metaplay dev dashboard' to serve the dashboard locally.
		`),
		Example: renderExample(`
			# Initialize the custom LiveOps Dashboard in the project.
			metaplay init dashboard
		`),
	}

	// Register flags.
	// flags := cmd.Flags()

	initCmd.AddCommand(cmd)
}

func (o *initDashboardOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func writePnpmWorkspaceFile(outfilePath string, entries []string) error {
	log.Debug().Msgf("Write pnpm-workspace.yaml with entries:")
	for _, entry := range entries {
		log.Debug().Msgf("  %s", entry)
	}
	data := struct {
		Packages []string `yaml:"packages"`
	}{
		Packages: entries,
	}
	bytes, err := yaml.Marshal(data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outfilePath, bytes, 0644); err != nil {
		return err
	}
	return nil
}

func (o *initDashboardOpts) Run(cmd *cobra.Command) error {
	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Initialize Custom LiveOps Dashboard in Your Project"))

	// Load project config.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	// Check that required dashboard tools are installed and satisfy version requirements.
	if err := checkDashboardToolVersions(project); err != nil {
		return err
	}

	// Resolve project dashboard dir (only Backend/Dashboard supported for now)
	dashboardDirRelative := filepath.ToSlash(filepath.Join(project.Config.BackendDir, "Dashboard"))

	// Install custom project from template in MetaplaySDK
	err = installFromTemplate(project, dashboardDirRelative, "dashboard_template.json")
	if err != nil {
		return fmt.Errorf("failed to run dashboard project installer: %v", err)
	}

	// Write pnpm-workspace.yaml
	if err = writePnpmWorkspaceFile(filepath.Join(project.RelativeDir, "pnpm-workspace.yaml"), []string{
		filepath.ToSlash(filepath.Join(project.Config.SdkRootDir, "Frontend", "*")),
		filepath.ToSlash(dashboardDirRelative),
	}); err != nil {
		return err
	}

	// Update metaplay-project.yaml to refer to newly initialized dashboard project
	err = updateProjectConfigCustomDashboard(project, dashboardDirRelative)
	if err != nil {
		return fmt.Errorf("failed to update metaplay-project.yaml: %v", err)
	}

	// Install dashboard dependencies (need to resolve the path in case '-p' was used to run this command)
	pathToDashboardDir := filepath.Join(project.RelativeDir, dashboardDirRelative)
	if err := execChildInteractive(pathToDashboardDir, "pnpm", []string{"install"}, nil); err != nil {
		log.Error().Msgf("Failed to run 'pnpm install': %s", err)
		os.Exit(1)
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Custom LiveOps Dashboard project setup successful!"))
	log.Info().Msg("")
	log.Info().Msg("The following changes were made to your project:")
	log.Info().Msgf("- Scaffolded dashboard project in %s", styles.RenderTechnical("Backend/Dashboard/"))
	log.Info().Msgf("- Updated %s to enable the custom dashboard", styles.RenderTechnical("metaplay-project.yaml"))
	log.Info().Msgf("- Added %s and %s to help pnpm find the projects", styles.RenderTechnical("pnpm-workspace.yaml"), styles.RenderTechnical("pnpm-lock.yaml"))
	log.Info().Msg("")
	log.Info().Msgf("Try running the dashboard locally with: %s", styles.RenderPrompt("metaplay dev dashboard"))

	return nil
}

// Update the metaplay-project.yaml features.dashboard section by enabling the custom dashboard
// and setting the dashboard root directory.
func updateProjectConfigCustomDashboard(project *metaproj.MetaplayProject, dashboardDir string) error {
	// Load the existing metaplay-project.yaml
	projectConfigFilePath := filepath.Join(project.RelativeDir, metaproj.ConfigFileName)
	configFileBytes, err := os.ReadFile(projectConfigFilePath)
	if err != nil {
		return fmt.Errorf("failed to read project config file: %v", err)
	}

	// Parse the YAML to AST
	root, err := parser.ParseBytes(configFileBytes, parser.ParseComments)
	if err != nil {
		panic(err)
	}

	// Update features.dashboard with new values.
	updateYamlNode(root, "$.features.dashboard", metaproj.DashboardFeatureConfig{
		UseCustom: true,
		RootDir:   dashboardDir,
	})

	// Write the updated YAML back to the file
	if err := os.WriteFile(projectConfigFilePath, []byte(root.String()), 0644); err != nil {
		return fmt.Errorf("failed to write updated config: %v", err)
	}

	return nil
}

// Replace a target node with 'value' (marshaled into YAML).
// Note: If the 'path' doesn't exist, this function does nothing.
func updateYamlNode(root *ast.File, path string, value interface{}) error {
	// Path to node to update.
	nodePath, err := yaml.PathString(path)
	if err != nil {
		return fmt.Errorf("failed to parse YAML path '%s': %w", path, err)
	}

	// Marshal the replacement to YAML.
	bytes, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal '%#v' node to YAML: %w", value, err)
	}

	// Update the target node with the new value.
	err = nodePath.MergeFromReader(root, strings.NewReader(string(bytes)))
	if err != nil {
		return fmt.Errorf("failed to update node in project config: %w", err)
	}

	return nil
}
