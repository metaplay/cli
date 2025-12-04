/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/common"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Support initializing projects with SDK versions up to this version.
// Only enforced when downloading the SDK from the portal.
var latestSupportedSdkVersion = version.Must(version.NewVersion("35.999.999"))

type initProjectOpts struct {
	flagProjectID          string // Human ID of the project.
	flagSdkVersion         string // Metaplay SDK version to use (e.g., "34.0").
	flagSdkSource          string // Path to Metaplay SDK release .zip to use.
	flagUnityProjectPath   string // Path to the Unity project files within the project.
	flagAutoAgreeContracts bool   // Automatically agree to the terms & conditions.
	flagAutoConfirm        bool   // Automatically confirm the 'Does this look correct?'
	flagNoSample           bool   // Skip installing the MetaplayHelloWorld sample.

	projectPath              string // User-provided path to project root (relative or absolute).
	absoluteProjectPath      string // Absolute path to the project root.
	relativeUnityProjectPath string // Relative path to the Unity project from the project root.
}

func init() {
	o := initProjectOpts{}

	cmd := &cobra.Command{
		Use:   "project [flags]",
		Short: "Initialize Metaplay SDK in an existing Unity project",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
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
		Example: renderExample(`
			# Initialize SDK in your project using the interactive wizard.
			metaplay init project

			# Initialize SDK in your project using a specific project ID.
			metaplay init project --project-id=fancy-gorgeous-bear

			# Initialize SDK in your project at a specific path.
			metaplay init project --project ../project-path

			# Specify Metaplay SDK version to use (only 34.0 and above are supported).
			metaplay init project --sdk-version=34.0

			# Use a pre-downloaded Metaplay SDK archive.
			metaplay init project --sdk-source=metaplay-sdk-release-34.0.zip
		`),
	}

	// Register flags.
	flags := cmd.Flags()
	flags.StringVar(&o.flagProjectID, "project-id", "", "The ID for your project, eg, 'fancy-gorgeous-bear' (optional)")
	flags.StringVar(&o.flagSdkVersion, "sdk-version", "", "Specify Metaplay SDK version to use, defaults to latest (optional)")
	flags.StringVar(&o.flagSdkSource, "sdk-source", "", "Install from the specified SDK archive file or use existing MetaplaySDK directory, eg, 'metaplay-sdk-release-34.0.zip' (optional)")
	flags.StringVar(&o.flagUnityProjectPath, "unity-project", "", "Path to the Unity project files within the project (default: auto-detect)")
	flags.BoolVar(&o.flagAutoAgreeContracts, "auto-agree", false, "Automatically agree to the privacy policy and terms and conditions")
	flags.BoolVar(&o.flagAutoConfirm, "yes", false, "Automatically confirm the 'Does this look correct?' confirmation")
	flags.BoolVar(&o.flagNoSample, "no-sample", false, "Skip installing the MetaplayHelloWorld sample scene")

	initCmd.AddCommand(cmd)
}

func (o *initProjectOpts) Prepare(cmd *cobra.Command, args []string) error {
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
	// Use default auth provider.
	// \todo ability to customize or disable provider?
	authProvider := auth.NewMetaplayAuthProvider()

	// Make sure the user is logged in.
	tokenSet, err := tui.RequireLoggedIn(cmd.Context(), authProvider)
	if err != nil {
		return err
	}

	metaplaySdkSource := ""
	if o.flagSdkSource != "" {
		metaplaySdkSource, err = filepath.Abs(o.flagSdkSource)
		if err != nil {
			return fmt.Errorf("Could not resolve sdk source location: %w", err)
		}
	}
	// Check if MetaplaySDK/ already exists: if so, we do migration only.
	if o.flagSdkSource == "" || !isDirectory(o.flagSdkSource) {
		metaplaySdkPath := filepath.Join(o.projectPath, "MetaplaySDK")
		_, err = os.Stat(metaplaySdkPath)
		if err == nil {
			return fmt.Errorf("MetaplaySDK/ directory already exists!")
		}
	}

	// If download SDK from portal, general terms & conditions must be approved for download to work.
	portalClient := portalapi.NewClient(tokenSet)
	var sdkVersionInfo *portalapi.SdkVersionInfo = nil
	var sdkVersionBadge string = ""
	if o.flagSdkSource == "" {
		// Ensure Privacy Policy is accepted.
		err = ensureSdkDownloadContractsAccepted(cmd.Context(), portalClient, o.flagAutoAgreeContracts)
		if err != nil {
			return err
		}

		// Resolve SDK version to download:
		// - If --sdk-version not specified, fetch the latest public release.
		// - If --sdk-version specified, try to match it from the available public releases.
		if o.flagSdkVersion == "" {
			// Get the latest SDK version info
			sdkVersionInfo, err = portalClient.GetLatestSdkVersionInfo()
			if err != nil {
				return fmt.Errorf("failed to get latest SDK version info: %w", err)
			}

			sdkVersionBadge = styles.RenderMuted("[latest]")
		} else {
			// Try to find the specified version
			sdkVersionInfo, err = portalClient.FindSdkVersionByVersionOrName(o.flagSdkVersion)
			if err != nil {
				return fmt.Errorf("failed to find SDK version '%s': %w", o.flagSdkVersion, err)
			}

			if sdkVersionInfo == nil {
				return fmt.Errorf("SDK version '%s' not found in Metaplay portal", o.flagSdkVersion)
			}
		}

		// Validate SDK version.
		// \todo Enforce version when using --sdk-source?
		_, err = parseAndValidateSdkVersion(sdkVersionInfo.Version)
		if err != nil {
			return err
		}

		// The SDK version must be downloadable.
		if sdkVersionInfo.StoragePath == nil {
			return fmt.Errorf("latest SDK version does not have a downloadable file")
		}
	}

	// Choose target project either with provided human ID or let user choose interactively.
	targetProject, err := chooseOrgAndProject(portalClient, o.flagProjectID)
	if err != nil {
		return err
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
	if sdkVersionInfo != nil {
		log.Info().Msgf("Metaplay version:   %s %s", styles.RenderTechnical(sdkVersionInfo.Version), sdkVersionBadge)
		log.Info().Msgf("Metaplay SDK dir:   %s%s", styles.RenderTechnical("MetaplaySDK"), styles.RenderAttention(" [new]"))
	} else if isDirectory(metaplaySdkSource) {
		log.Info().Msgf("Metaplay SDK:       %s %s", styles.RenderTechnical(metaplaySdkSource), styles.RenderAttention("[use existing]"))
	} else {
		log.Info().Msgf("Metaplay SDK:       %s", styles.RenderTechnical(metaplaySdkSource))
		log.Info().Msgf("Metaplay SDK dir:   %s%s", styles.RenderTechnical("MetaplaySDK"), styles.RenderAttention(" [new]"))
	}
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
	runner.AddTask("Download & extract Metaplay SDK", func(output *tui.TaskOutput) error {
		// Resolve the SDK source to use based on --sdk-source flag:
		// - If not specified, download .zip from portal and extract it to project directory.
		// - If points to a Metaplay release .zip file, extract it to project directory.
		// - If points to a MetaplaySDK directory, refer to the directory (don't copy).
		if metaplaySdkSource == "" {
			// Download and extract to target project dir.
			relativePathToSdk = "MetaplaySDK"
			sdkMetadata, err = downloadAndExtractSdk(tokenSet, o.absoluteProjectPath, sdkVersionInfo)
			if err != nil {
				return err
			}
		} else {
			relativePathToSdk, sdkMetadata, err = resolveSdkSource(o.absoluteProjectPath, metaplaySdkSource)
			if err != nil {
				return err
			}
		}
		log.Debug().Msgf("Relative path to MetaplaySDK (from project root): %s", relativePathToSdk)

		return nil
	})

	// Generate the metaplay-project.yaml in project root.
	var projectConfig *metaproj.ProjectConfig
	runner.AddTask("Generate metaplay-project.yaml", func(output *tui.TaskOutput) error {
		projectConfig, err = metaproj.GenerateProjectConfigFile(
			sdkMetadata,
			o.absoluteProjectPath,
			o.relativeUnityProjectPath,
			relativePathToSdk,
			filepath.Join(o.relativeUnityProjectPath, "Assets", "SharedCode"),
			"Backend", // game backend dir
			"",        // game dashboard dir
			targetProject,
			environments)
		return err
	})

	// Only extract files if not migrating existing project
	runner.AddTask("Update files to project", func(output *tui.TaskOutput) error {
		// Create MetaplayProject.
		project, err := metaproj.NewMetaplayProject(o.projectPath, projectConfig, sdkMetadata)
		if err != nil {
			return err
		}

		// Copy files from the template.
		log.Debug().Msgf("Initialize SDK resources in the project")
		err = installFromTemplate(project, ".", "project_template.json", map[string]string{
			"PROJECT_HUMAN_ID":          targetProject.HumanID,
			"PROJECT_DISPLAY_NAME":      targetProject.Name,    // Added in R34
			"PROJECT_NAME":              targetProject.HumanID, // Remove in R34
			"BACKEND_SOLUTION_FILENAME": "Server.sln",
		}, o.flagNoSample)
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
	log.Info().Msgf("- Added shared game logic code at %s", styles.RenderTechnical(filepath.ToSlash(filepath.Join(o.relativeUnityProjectPath, "Assets/SharedCode/"))))
	if !o.flagNoSample {
		log.Info().Msgf("- Added sample scene in %s", styles.RenderTechnical(filepath.ToSlash(filepath.Join(o.relativeUnityProjectPath, "Assets/MetaplayHelloWorld/"))))
	}
	log.Info().Msgf("- Added pre-built game config archive to %s", styles.RenderTechnical(filepath.ToSlash(filepath.Join(o.relativeUnityProjectPath, "Assets/StreamingAssets/"))))
	log.Info().Msgf("- Added reference to Metaplay Client SDK in %s", styles.RenderTechnical(filepath.ToSlash(filepath.Join(o.relativeUnityProjectPath, "Packages/manifest.json"))))

	return nil
}

// ensureSdkDownloadContractsAccepted ensures that the user has agreed to the Privacy Policy and Terms & Conditions
// before SDK download. If auto-agree is specified, the contracts are accepted automatically, otherwise the user is
// prompted to agree to the contracts.
func ensureSdkDownloadContractsAccepted(ctx context.Context, portalClient *portalapi.Client, autoAgree bool) error {
	log.Debug().Msg("Fetch the user state to see which contracts have been signed")
	userState, err := portalClient.GetUserState()
	if err != nil {
		return err
	}

	if err := ensureContractAccepted(ctx, portalClient, &userState.Contracts.PrivacyPolicy, autoAgree); err != nil {
		return err
	}
	if err := ensureContractAccepted(ctx, portalClient, &userState.Contracts.TermsAndConditions, autoAgree); err != nil {
		return err
	}
	return nil
}

// ensureContractAccepted ensures that the specified contract has been signed by the user. If the contract is not
// yet accepted, the user is prompted to do so, or if auto-agree is specified, the contract is accepted automatically.
func ensureContractAccepted(ctx context.Context, portalClient *portalapi.Client, contractState *portalapi.UserCoreContract, autoAgree bool) error {
	// If already signed, return immediately.
	if contractState.ContractSignature != nil {
		return nil
	}

	// If auto-agree not specified, confirm the user for agreement to contract.
	if !autoAgree {
		contractURL := fmt.Sprintf("%s/contracts/%s", common.PortalBaseURL, contractState.ID)
		choice, err := tui.DoConfirmDialog(
			ctx,
			fmt.Sprintf("Agree to %s", contractState.Name),
			fmt.Sprintf("You need to agree to the Metaplay %s in order to download the Metaplay SDK:\n  %s", contractState.Name, contractURL),
			"Do you agree?")
		if err != nil {
			return err
		}

		if !choice {
			return fmt.Errorf("refused to accept %s; unable to download Metaplay SDK", contractState.Name)
		}
	}

	// Update agreed-to status in portal
	err := portalClient.AgreeToContract(contractState.ID)
	if err != nil {
		return err
	}

	log.Info().Msgf(" %s Agreed to %s!", styles.RenderSuccess("✓"), contractState.Name)
	return nil
}

// Parse an SDK version and validate that it's compatible with this CLI version.
func parseAndValidateSdkVersion(versionStr string) (*version.Version, error) {
	// Validate the selected SDK version.
	vsn, err := version.NewVersion(versionStr)
	if err != nil {
		return nil, fmt.Errorf("invalid SDK version string '%s': %w", versionStr, err)
	}

	// Check that the SDK version is the minimum supported by the CLI.
	if vsn.LessThan(metaproj.OldestSupportedSdkVersion) {
		return nil, fmt.Errorf("SDK version %s is too old; this operation only works with SDK versions %s and above", versionStr, metaproj.OldestSupportedSdkVersion)
	}

	// Must not be newer than the latest supported version.
	if vsn.GreaterThan(latestSupportedSdkVersion) {
		return nil, fmt.Errorf("SDK version %s is not supported by this CLI version; upgrade the CLI to the latest version with 'metaplay update cli'", versionStr)
	}

	return vsn, nil
}
