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
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/filesetwriter"
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

// renderPnpmWorkspaceContent renders a pnpm-workspace.yaml with the given package entries.
func renderPnpmWorkspaceContent(entries []string) ([]byte, error) {
	log.Debug().Msgf("Render pnpm-workspace.yaml with entries:")
	for _, entry := range entries {
		log.Debug().Msgf("  %s", entry)
	}
	data := struct {
		Packages []string `yaml:"packages"`
	}{
		Packages: entries,
	}
	return yaml.Marshal(data)
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

	// Build a plan with all files to write
	plan := filesetwriter.NewPlan().SetInteractive(tui.IsInteractiveMode())

	// Collect template files into the plan
	err = collectFromTemplate(plan, project, dashboardDirRelative, "dashboard_template.json", map[string]string{}, false)
	if err != nil {
		return fmt.Errorf("failed to collect dashboard template files: %v", err)
	}

	// Render pnpm-workspace.yaml content
	pnpmContent, err := renderPnpmWorkspaceContent([]string{
		filepath.ToSlash(filepath.Join(project.Config.SdkRootDir, "Frontend", "*")),
		filepath.ToSlash(dashboardDirRelative),
	})
	if err != nil {
		return fmt.Errorf("failed to render pnpm-workspace.yaml: %v", err)
	}
	plan.Add(filepath.Join(project.RelativeDir, "pnpm-workspace.yaml"), pnpmContent, 0644)

	// Compute updated metaplay-project.yaml content
	configPath, configContent, err := computeProjectConfigDashboardUpdate(project, dashboardDirRelative)
	if err != nil {
		return fmt.Errorf("failed to compute metaplay-project.yaml update: %v", err)
	}
	plan.Add(configPath, configContent, 0644)

	// Scan the filesystem and show file preview
	if err := plan.Scan(); err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Project Modification Summary"))
	log.Info().Msg("")
	plan.Preview()

	if plan.HasReadOnlyFiles() {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderWarning("Some files are read-only and may need 'p4 edit'."))
	}

	// Confirm before writing.
	if tui.IsInteractiveMode() {
		log.Info().Msg("")
		confirmed, err := tui.DoConfirmQuestion(cmd.Context(), "Proceed?")
		if err != nil {
			return err
		}
		if !confirmed {
			log.Info().Msg("Aborted.")
			return nil
		}
	}

	// Write all files at once
	if err := plan.Execute(); err != nil {
		return err
	}

	// Install dashboard dependencies (need to resolve the path in case '-p' was used to run this command)
	pathToDashboardDir := filepath.Join(project.RelativeDir, dashboardDirRelative)
	if err := execChildInteractive(pathToDashboardDir, "pnpm", []string{"install"}, nil); err != nil {
		return clierrors.Wrap(err, "Failed to run 'pnpm install'").
			WithSuggestion("Check that pnpm is installed and try running 'pnpm install' manually")
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

// computeProjectConfigDashboardUpdate reads the metaplay-project.yaml, enables the custom
// dashboard in the features.dashboard section, and returns the updated content without writing.
func computeProjectConfigDashboardUpdate(project *metaproj.MetaplayProject, dashboardDir string) (string, []byte, error) {
	// Load the existing metaplay-project.yaml
	projectConfigFilePath := filepath.Join(project.RelativeDir, metaproj.ConfigFileName)
	configFileBytes, err := os.ReadFile(projectConfigFilePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read project config file: %v", err)
	}

	// Parse the YAML to AST
	root, err := parser.ParseBytes(configFileBytes, parser.ParseComments)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse project config file: %v", err)
	}

	// Update features.dashboard with new values.
	updateYamlNode(root, "$.features.dashboard", metaproj.DashboardFeatureConfig{
		UseCustom: true,
		RootDir:   dashboardDir,
	})

	return projectConfigFilePath, []byte(root.String()), nil
}

// Replace a target node with 'value' (marshaled into YAML).
// Note: If the 'path' doesn't exist, this function does nothing.
func updateYamlNode(root *ast.File, path string, value any) error {
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
