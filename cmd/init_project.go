/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Link to the terms & conditions required to download the SDK
// \todo add test to see that this URL is valid
const metaplayTermsAndConditionsUrl = "https://portal.metaplay.dev/licenses/general-terms-and-conditions"

type initProjectOpts struct {
	flagProjectID                   string // Human ID of the project.
	flagSdkVersion                  string // Metaplay SDK version to use (e.g., "32.0").
	flagSdkSource                   string // Path to Metaplay SDK release .zip to use.
	flagUnityProjectPath            string // Path to the Unity project files within the project.
	flagAutoAgreeTermsAndConditions bool   // Automatically agree to the terms & conditions.
	flagAutoConfirm                 bool   // Automatically confirm the 'Does this look correct?'

	projectPath              string // User-provided path to project root (relative or absolute).
	absoluteProjectPath      string // Absolute path to the project root.
	relativeUnityProjectPath string // Relative path to the Unity project from the project root.
}

func init() {
	o := &initProjectOpts{}

	cmd := &cobra.Command{
		Use:   "project [flags]",
		Short: "Initialize Metaplay SDK in an existing Unity project",
		Run:   runCommand(o),
		Long: trimIndent(`
			Integrate Metaplay SDK into an existing project.

			By default, this command downloads the latest Metaplay SDK from the portal. You must be
			logged in (using 'metaplay auth login') and have signed the SDK terms and conditions in
			the portal (https://portal.metaplay.dev).

			This command does the following:
			1. Extract the Metaplay SDK contents into MetaplaySDK/.
			2. Initialize the following in your project:
			  - metaplay-project.yaml ...
			  - <unity-project>/Assets/MetaplayHelloWorld
			  - <unity-project>/Assets/SharedCode
			  - <unity-project>/Assets/StreamingAssets/...
			  - Backend/
			3. Add reference to the Metaplay Client SDK to your Unity project package.json.

			Related commands:
			- 'metaplay build image' builds a docker image to be deployed to the cloud.
			- 'metaplay update project-environments' updates the environments list in metaplay-project.yaml from the cloud.
			- 'metaplay init dashboard' initializes custom LiveOps Dashboard in the project.
		`),
		Example: trimIndent(`
			# Initialize SDK in your project using the interactive wizard.
			metaplay init project

			# Initialize SDK in your project using a specific project ID.
			metaplay init project --project-id=fancy-gorgeous-bear

			# Initialize SDK in your project at a specific path.
			metaplay init project --project ../project-path

			# Specify Metaplay SDK version to use (only 32.0 and above are supported).
			metaplay init project --sdk-version=32.0

			# Use a pre-downloaded Metaplay SDK archive.
			metaplay init project --sdk-source=metaplay-sdk-release-32.0.zip
		`),
	}

	// Register flags.
	flags := cmd.Flags()
	flags.StringVar(&o.flagProjectID, "project-id", "", "The ID for your project, eg, 'fancy-gorgeous-bear' (optional)")
	flags.StringVar(&o.flagSdkVersion, "sdk-version", "", "Specify Metaplay SDK version to use, defaults to latest (optional)")
	flags.StringVar(&o.flagSdkSource, "sdk-source", "", "Install from the specified SDK archive file or use existing MetaplaySDK directory, eg, 'metaplay-sdk-release-32.0.zip' (optional)")
	flags.StringVar(&o.flagUnityProjectPath, "unity-project", "", "Path to the Unity project files within the project (default: auto-detect)")
	flags.BoolVar(&o.flagAutoAgreeTermsAndConditions, "auto-agree", false, fmt.Sprintf("Automatically agree to the terms & conditions (%s)", metaplayTermsAndConditionsUrl))
	flags.BoolVar(&o.flagAutoConfirm, "yes", false, "Automatically confirm to the 'Does this look correct?' confirmation")

	initCmd.AddCommand(cmd)
}

func (o *initProjectOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("not expecting any arguments, got %d", len(args))
	}

	// Resolve target project root directory (where metaplay-project.yaml is created).
	o.projectPath = coalesceString(flagProjectConfigPath, ".")

	// Resolve absolute project root path.
	var err error
	o.absoluteProjectPath, err = filepath.Abs(o.projectPath)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute project path: %w", err)
	}

	// Validate project ID (if specified)
	if o.flagProjectID != "" {
		if err := metaproj.ValidateProjectID(o.flagProjectID); err != nil {
			return err
		}
	}

	// --sdk-version and --sdk-source are mutually exclusive.
	if o.flagSdkVersion != "" && o.flagSdkSource != "" {
		return fmt.Errorf("--sdk-version and --sdk-source are mutually exclusive; only one can be used at a time")
	}

	// Check if metaplay-project.yaml already exists
	configFilePath := filepath.Join(o.projectPath, metaproj.ConfigFileName)
	if _, err := os.Stat(configFilePath); err == nil {
		return fmt.Errorf("config file %s exists, project has already been initialized", configFilePath)
	}

	// If Unity project path is not specified, try to find it within the project.
	if o.flagUnityProjectPath == "" {
		relativeUnityPath, err := findUnityProjectPath(o.absoluteProjectPath)
		if err != nil {
			return err
		}
		o.relativeUnityProjectPath = relativeUnityPath
	} else {
		o.relativeUnityProjectPath = o.flagUnityProjectPath
	}

	// Validate the Unity project path
	if err := validateUnityProjectPath(o.absoluteProjectPath, o.relativeUnityProjectPath); err != nil {
		return err
	}

	// Must be either in interactive mode or specify --yes.
	if !tui.IsInteractiveMode() && !o.flagAutoConfirm {
		return fmt.Errorf("use --yes to automatically confirm changes when running in non-interactive mode")
	}

	return nil
}

func (o *initProjectOpts) Run(cmd *cobra.Command) error {
	// Make sure the user is logged in.
	tokenSet, err := tui.RequireLoggedIn(cmd.Context())
	if err != nil {
		return err
	}

	// Check if MetaplaySDK/ already exists: if so, we do migration only.
	metaplaySdkPath := filepath.Join(o.projectPath, "MetaplaySDK")
	metaplaySdkSource := o.flagSdkSource
	_, err = os.Stat(metaplaySdkPath)
	if err == nil {
		return fmt.Errorf("MetaplaySDK/ directory already exists!")
	}

	// If download SDK from portal, general terms & conditions must be approved for download to work.
	portalClient := portalapi.NewClient(tokenSet)
	if o.flagSdkSource == "" {
		// Handle agreeing to general terms & conditions.
		log.Debug().Msg("Check whether terms and conditions has been signed")
		hasSigned, err := portalClient.IsGeneralTermsAndConditionsAccepted()
		if err != nil {
			return err
		}

		// If hasn't agreed to the terms & conditions, let the user do so.
		if !hasSigned {
			if !o.flagAutoAgreeTermsAndConditions {
				choice, err := tui.DoConfirmDialog(
					cmd.Context(),
					"Agree to General Terms & Conditions",
					fmt.Sprintf("You need to agree to the Metaplay General Terms & Conditions before you can download the Metaplay SDK:\n  %s", metaplayTermsAndConditionsUrl),
					"Do you agree?")
				if err != nil {
					return fmt.Errorf("failed to agree to terms & conditions: %w", err)
				}

				if !choice {
					return fmt.Errorf("the user did not agree to the terms & conditions; unable to download Metaplay SDK")
				}
			}

			// Update agreed-to status in portal
			err = portalClient.AgreeToGeneralTermsAndConditions()
			if err != nil {
				return err
			}

			log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), "Status updated in portal!")
		}
	}

	// Choose target project either with human ID provided as flag or interactively
	// let the user choose from a list of projects fetched from the portal.
	var targetProject *portalapi.ProjectInfo
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

	// Fetch all project's environments (for populating the metaplay-config.yaml).
	environments, err := portalClient.FetchProjectEnvironments(targetProject.UUID)
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Integrate Metaplay SDK to Your Project"))
	log.Info().Msg("")

	log.Info().Msgf("Project:            %s %s", styles.RenderTechnical(targetProject.Name), styles.RenderMuted(fmt.Sprintf("[%s]", targetProject.HumanID)))
	log.Info().Msgf("Project root:       %s", styles.RenderTechnical(o.absoluteProjectPath))
	log.Info().Msgf("Unity project dir:  %s", styles.RenderTechnical(filepath.Join(o.absoluteProjectPath, o.relativeUnityProjectPath)))
	log.Info().Msgf("Metaplay SDK:       %s", styles.RenderTechnical(coalesceString(metaplaySdkSource, o.flagSdkVersion, "latest")))
	log.Info().Msgf("Metaplay SDK dir:   %s%s", styles.RenderTechnical("MetaplaySDK"), styles.RenderAttention(" [new]"))
	log.Info().Msgf("Game backend dir:   %s%s", styles.RenderTechnical("Backend"), styles.RenderAttention(" [new]"))
	log.Info().Msg("")

	// Confirm from the user that the proposed operation looks correct.
	if !o.flagAutoConfirm {
		isOk, err := tui.DoConfirmQuestion(cmd.Context(), "Does this look correct?")
		if err != nil {
			return err
		}
		if !isOk {
			log.Info().Msg(styles.RenderError("❌ Operation canceled"))
			return nil
		}
	}

	// Create task runner
	runner := tui.NewTaskRunner()

	var relativePathToSdk string
	var sdkMetadata *metaproj.MetaplayVersionMetadata

	// Only download/extract SDK if not migrating existing project
	runner.AddTask("Download & extract Metaplay SDK", func() error {
		// Resolve the SDK source to use based on --sdk-source flag:
		// - If not specified, download .zip from portal and extract it to project directory.
		// - If points to a Metaplay release .zip file, extract it to project directory.
		// - If points to a MetaplaySDK directory, refer to the directory (don't copy).
		if o.flagSdkSource == "" {
			// Download and extract to target project dir.
			relativePathToSdk = "MetaplaySDK"
			sdkMetadata, err = downloadAndExtractSdk(tokenSet, o.absoluteProjectPath, o.flagSdkVersion)
			if err != nil {
				return err
			}
		} else {
			relativePathToSdk, sdkMetadata, err = resolveSdkSource(o.absoluteProjectPath, o.flagSdkSource)
			if err != nil {
				return err
			}
		}
		log.Debug().Msgf("Relative path to MetaplaySDK: %s", relativePathToSdk)

		return nil
	})

	// Generate the metaplay-project.yaml in project root.
	var projectConfig *metaproj.ProjectConfig
	runner.AddTask("Generate metaplay-project.yaml", func() error {
		projectConfig, err = metaproj.GenerateProjectConfigFile(sdkMetadata, o.absoluteProjectPath, o.relativeUnityProjectPath, relativePathToSdk, targetProject, environments)
		return err
	})

	// Only extract files if not migrating existing project
	runner.AddTask("Update files to project", func() error {
		// Create MetaplayProject.
		project, err := metaproj.NewMetaplayProject(o.projectPath, projectConfig, sdkMetadata)
		if err != nil {
			return err
		}

		// Copy files from the template.
		log.Debug().Msgf("Initialize SDK resources in the project")
		err = installFromTemplate(project, ".", "project_template.json")
		if err != nil {
			return fmt.Errorf("failed to run SDK installer: %w", err)
		}

		// Add MetaplaySDK to the Unity project package.json.
		log.Debug().Msgf("Add reference to Metaplay Client SDK to project's Unity manifest.json..")
		err = addReferenceToUnityManifest(project)
		if err != nil {
			return err
		}

		return nil
	})

	// Run the tasks
	if err := runner.Run(); err != nil {
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ SDK integrated successfully!"))
	log.Info().Msg("")
	log.Info().Msg("The following changes were made to your project:")
	log.Info().Msgf("- Added project configuration file %s", styles.RenderTechnical("metaplay-project.yaml"))
	log.Info().Msgf("- Added shared game logic code at %s", styles.RenderTechnical("UnityClient/Assets/SharedCode/"))
	log.Info().Msgf("- Added sample scene in %s", styles.RenderTechnical("UnityClient/Assets/MetaplayHelloWorld/"))
	log.Info().Msgf("- Added pre-built game config archive to %s", styles.RenderTechnical("UnityClient/Assets/StreamingAssets/"))
	log.Info().Msgf("- Added reference to Metaplay Client SDK in %s", styles.RenderTechnical("UnityClient/Package/manifest.json"))

	return nil
}
