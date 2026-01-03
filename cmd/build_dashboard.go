/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type buildDashboardOpts struct {
	UsePositionalArgs

	extraArgs          []string
	flagSkipInstall    bool // Skip 'pnpm install'
	flagOutputPrebuilt bool // Output to 'Backend/PrebuiltDashboard/' -- \todo Auto-detect this from metaplay-project.yaml in the future
}

func init() {
	o := buildDashboardOpts{}

	args := o.Arguments()
	args.SetExtraArgs(&o.extraArgs, "Passed as-is to 'pnpm build'.")

	var buildDashboardCmd = &cobra.Command{
		Use:     "dashboard [flags]",
		Aliases: []string{"dash"},
		Short:   "Build the Vue.js LiveOps Dashboard",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Build the Vue.js LiveOps Dashboard locally.

			This command first checks that Node.js and pnpm are installed and satisfy
			version requirements. Then it installs dashboard dependencies (unless
			--skip-install is used) and builds the dashboard.

			The build process includes TypeScript compilation and Vite bundling.
			By default, the output is placed in Backend/Dashboard/dist/. The locally running
			game server (with 'metaplay run server') will serve this output on http://localhost:5550.

			If you want to include a pre-built version of the dashboard in your version
			control, so that it can be served locally without the Node/pnpm tooling installed,
			use the --output-prebuilt flag to place the build output in Backend/PrebuiltDashboard/.
			If you do this, you should commit the Backend/PrebuiltDashboard/ directory to
			version control.

			Related commands:
			- 'metaplay build server' builds the game server .NET project.
			- 'metaplay build image' builds a Docker image with the server and dashboard.
			- 'metaplay dev dashboard' runs the dashboard in development mode.
		`),
		Example: renderExample(`
			# Build the dashboard.
			metaplay build dashboard

			# Output pre-built dashboard (see help text for explanations)
			metaplay build dashboard --output-prebuilt

			# Skip dependency installation (faster builds if deps already installed)
			metaplay build dashboard --skip-install

			# Pass extra arguments to vite build
			metaplay build dashboard -- --mode production
		`),
	}

	buildDashboardCmd.Flags().BoolVar(&o.flagSkipInstall, "skip-install", false, "Skip the pnpm install step")
	buildDashboardCmd.Flags().BoolVar(&o.flagSkipInstall, "skip-pnpm", false, "Skip the pnpm install step (deprecated, use --skip-install)")
	buildDashboardCmd.Flags().BoolVar(&o.flagOutputPrebuilt, "output-prebuilt", false, "Output pre-built version of the dashboard (see help text)")

	buildCmd.AddCommand(buildDashboardCmd)
}

func (o *buildDashboardOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Check if the deprecated --skip-pnpm flag was used and show warning
	if cmd.Flags().Changed("skip-pnpm") {
		log.Warn().Msg("Warning: --skip-pnpm is deprecated, please use --skip-install instead")
	}
	return nil
}

func (o *buildDashboardOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	// Check that project uses a custom dashboard, otherwise error out
	if !project.UsesCustomDashboard() {
		return fmt.Errorf("project does not have a custom dashboard to build")
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Build LiveOps Dashboard Locally"))
	log.Info().Msg("")

	// Show output directory.
	outputDir := "Backend/Dashboard/dist"
	if o.flagOutputPrebuilt {
		outputDir = "Backend/PrebuiltDashboard"
	}
	log.Info().Msgf("Output directory: %s", styles.RenderTechnical(outputDir))
	log.Info().Msg("")

	// Check that required dashboard tools are installed and satisfy version requirements.
	if err := checkDashboardToolVersions(project); err != nil {
		return err
	}

	// Resolve project dashboard path.
	dashboardPath := project.GetDashboardDir()

	// Install dashboard dependencies if not skipped.
	if !o.flagSkipInstall {
		installArgs := []string{"install"}
		log.Info().Msg("Install dashboard dependencies...")
		log.Info().Msg(styles.RenderMuted(fmt.Sprintf("> pnpm %s", strings.Join(installArgs, " "))))
		if err := execChildInteractive(dashboardPath, "pnpm", installArgs, nil); err != nil {
			log.Error().Msgf("Failed to install LiveOps Dashboard dependencies: %s", err)
			os.Exit(1)
		}
	} else {
		log.Info().Msg("Skipping pnpm install because of the --skip-pnpm flag")
	}

	// Build with pnpm. If --output-prebuilt flag is set, output build results to Backend/PrebuiltDashboard,
	// intended to be committed to version control.
	buildArgs := []string{"build"}
	if o.flagOutputPrebuilt {
		buildArgs = append(buildArgs, "--outDir", "../PrebuiltDashboard")
	}
	buildArgs = append(buildArgs, o.extraArgs...)
	log.Info().Msg("")
	log.Info().Msg("Build dashboard...")
	log.Info().Msg(styles.RenderMuted(fmt.Sprintf("> pnpm %s", strings.Join(buildArgs, " "))))
	err = execChildInteractive(dashboardPath, "pnpm", buildArgs, nil)
	if err != nil {
		log.Error().Msgf("Failed to build the LiveOps Dashboard: %s", err)
		os.Exit(1)
	}

	// Build done.
	log.Info().Msg("")
	log.Info().Msgf("✅ Dashboard built successfully")
	log.Info().Msg("")
	if o.flagOutputPrebuilt {
		log.Info().Msgf("%s", styles.RenderPrompt("You should commit the modified files in Backend/PrebuiltDashboard/ to your version control."))
	} else {
		log.Info().Msg("The game server will serve the built dashboard on http://localhost:5550")
	}
	return nil
}
