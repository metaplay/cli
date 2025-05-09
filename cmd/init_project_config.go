/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type initProjectConfigOpts struct {
	flagProjectID         string // Human ID of the project.
	flagUnityProjectPath  string // Path to the Unity project files within the project.
	flagMetaplaySdkPath   string // Path to the MetaplaySDK directory
	flagGameBackendPath   string // Path to the game backend directory
	flagGameDashboardPath string // Path to the game dashboard directory
	flagSharedCodePath    string // Path to the shared code directory
	flagDotnetRuntimeVer  string // .NET runtime version
	flagAutoConfirm       bool   // Automatically confirm the 'Does this look correct?'

	projectPath              string // User-provided path to project root (relative or absolute).
	absoluteProjectPath      string // Absolute path to the project root.
	relativeUnityProjectPath string // Relative path to the Unity project from the project root.
}

// Detected project configuration. All paths are relative to the project root.
type detectedProjectConfig struct {
	metaplaySdkPath      string
	gameBackendPath      string
	gameDashboardPath    string
	unityProjectPath     string
	sharedCodePath       string
	dotnetRuntimeVersion string
}

func init() {
	o := initProjectConfigOpts{}

	cmd := &cobra.Command{
		Use:   "project-config [flags]",
		Short: "Initialize the metaplay-project.yaml in an existing project",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Initialize a metaplay-project.yaml configuration file in an existing project directory.
			This file is used by the CLI to understand the project structure and configuration.

			The command will auto-detect various project paths and settings:
			- Unity project location
			- MetaplaySDK directory
			- Game backend directory
			- Game dashboard directory (if present)
			- Shared code directory
			- .NET runtime version

			The detected paths can be overridden using command-line flags if needed.
			All paths are stored relative to the project root directory.

			After detection, the command will:
			1. Validate the detected paths and settings
			2. Show a summary of what was found
			3. Ask for confirmation (unless --yes is specified)
			4. Create the metaplay-project.yaml file

			Requires Metaplay SDK 32.0 or later to be updated in your project repository.

			Related commands:
			- 'metaplay init project' to create a new project from scratch
			- 'metaplay update project-environments' to update environment configurations
		`),
		Example: renderExample(`
			# Generate the in your project.
			metaplay init project-config

			# Specify the project ID.
			metaplay init project-config --project-id=lovely-wombats-build

			# Auto-approve the operation.
			metaplay init project-config --yes
		`),
	}

	// Register flags.
	flags := cmd.Flags()
	flags.StringVar(&o.flagProjectID, "project-id", "", "The ID for your project, eg, 'fancy-gorgeous-bear' (optional)")
	flags.StringVar(&o.flagUnityProjectPath, "unity-project", "", "Path to the Unity project files within the project (default: auto-detect)")
	flags.StringVar(&o.flagMetaplaySdkPath, "sdk-path", "", "Path to the MetaplaySDK directory (default: auto-detect)")
	flags.StringVar(&o.flagGameBackendPath, "backend-path", "", "Path to the game backend directory (default: auto-detect)")
	flags.StringVar(&o.flagGameDashboardPath, "dashboard-path", "", "Path to the game dashboard directory (default: auto-detect)")
	flags.StringVar(&o.flagSharedCodePath, "shared-code-path", "", "Path to the shared code directory (default: auto-detect)")
	flags.StringVar(&o.flagDotnetRuntimeVer, "dotnet-version", "", ".NET runtime version (default: auto-detect)")
	flags.BoolVar(&o.flagAutoConfirm, "yes", false, "Automatically confirm to the 'Does this look correct?' confirmation")

	initCmd.AddCommand(cmd)
}

func (o *initProjectConfigOpts) Prepare(cmd *cobra.Command, args []string) error {
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

	// Must be either in interactive mode or specify --yes.
	if !tui.IsInteractiveMode() && !o.flagAutoConfirm {
		return fmt.Errorf("use --yes to automatically confirm changes when running in non-interactive mode")
	}

	return nil
}

func (o *initProjectConfigOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Always use Metaplay Auth for project initialization.
	authProvider, err := getAuthProvider(project, "metaplay")
	if err != nil {
		return err
	}

	// Make sure the user is logged in.
	tokenSet, err := tui.RequireLoggedIn(cmd.Context(), authProvider)
	if err != nil {
		return err
	}

	// Check if metaplay-project.yaml already exists
	configFilePath := filepath.Join(o.projectPath, metaproj.ConfigFileName)
	if _, err := os.Stat(configFilePath); err == nil {
		return fmt.Errorf("project config file %s already exists", configFilePath)
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
	portalClient := portalapi.NewClient(tokenSet)
	environments, err := portalClient.FetchProjectEnvironments(targetProject.UUID)
	if err != nil {
		return err
	}

	// Detect project paths
	projectConfig, err := o.detectProjectConfig()
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Initialize Project Config"))
	log.Info().Msg("")

	log.Info().Msgf("Project:              %s %s", styles.RenderTechnical(targetProject.Name), styles.RenderMuted(fmt.Sprintf("[%s]", targetProject.HumanID)))
	log.Info().Msgf("Project root:         %s", styles.RenderTechnical(o.absoluteProjectPath))
	log.Info().Msgf("Unity project dir:    %s", styles.RenderTechnical(projectConfig.unityProjectPath))
	log.Info().Msgf("Metaplay SDK dir:     %s", styles.RenderTechnical(projectConfig.metaplaySdkPath))
	log.Info().Msgf("Shared code dir:      %s", styles.RenderTechnical(projectConfig.sharedCodePath))
	log.Info().Msgf("Game backend dir:     %s", styles.RenderTechnical(projectConfig.gameBackendPath))
	log.Info().Msgf("Game dashboard dir:   %s", styles.RenderTechnical(coalesceString(projectConfig.gameDashboardPath, "n/a")))
	log.Info().Msgf(".NET runtime version: %s", styles.RenderTechnical(projectConfig.dotnetRuntimeVersion))
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

	// Resolve the SDK metadata from existing MetaplaySDK directory
	sdkMetadata, err := validateSdkDirectory(filepath.Join(o.absoluteProjectPath, projectConfig.metaplaySdkPath))
	if err != nil {
		return err
	}

	// Generate the metaplay-project.yaml in project root.
	_, err = metaproj.GenerateProjectConfigFile(
		sdkMetadata,
		o.absoluteProjectPath,
		o.relativeUnityProjectPath,
		projectConfig.metaplaySdkPath,
		projectConfig.sharedCodePath,
		projectConfig.gameBackendPath,
		projectConfig.gameDashboardPath,
		targetProject,
		environments)
	if err != nil {
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ Project config file 'metaplay-project.yaml' created!"))
	return nil
}

// Detect the project configuration from its files.
func (o *initProjectConfigOpts) detectProjectConfig() (*detectedProjectConfig, error) {
	var metaplaySdkPath string
	var err error

	// Use flag value if provided, otherwise auto-detect
	if o.flagMetaplaySdkPath != "" {
		metaplaySdkPath = o.flagMetaplaySdkPath
	} else {
		metaplaySdkPath, err = findSubDirectory("Metaplay SDK", o.absoluteProjectPath, func(rootPath, relPath string) (bool, error) {
			// Check directory name
			if filepath.Base(relPath) != "MetaplaySDK" {
				return false, nil
			}

			// Check for required files
			dockerfilePath := filepath.Join(rootPath, relPath, "Dockerfile.server")
			versionPath := filepath.Join(rootPath, relPath, "version.yaml")

			// Check if files exist
			if _, err := os.Stat(dockerfilePath); err != nil {
				return false, nil
			}
			if _, err := os.Stat(versionPath); err != nil {
				return false, nil
			}

			return true, nil
		})
		if err != nil {
			return nil, err
		}
	}

	// Find game backend directory.
	var gameBackendPath string
	if o.flagGameBackendPath != "" {
		gameBackendPath = o.flagGameBackendPath
	} else {
		gameBackendPath, err = findSubDirectory("game-specific backend", o.absoluteProjectPath, func(rootPath, relPath string) (bool, error) {
			// Check directory name
			dirName := filepath.Base(relPath)
			if dirName != "Backend" && dirName != "Server" {
				return false, nil
			}

			// Check for required files
			globalJSONPath := filepath.Join(rootPath, relPath, "global.json")
			buildPropsPath := filepath.Join(rootPath, relPath, "Directory.Build.props")

			// Check if files exist
			if _, err := os.Stat(globalJSONPath); err != nil {
				return false, nil
			}
			if _, err := os.Stat(buildPropsPath); err != nil {
				return false, nil
			}

			// Read Directory.Build.props content
			buildPropsContent, err := os.ReadFile(buildPropsPath)
			if err != nil {
				return false, nil
			}

			// Check for required text in Directory.Build.props
			buildPropsStr := string(buildPropsContent)
			if !strings.Contains(buildPropsStr, "<MetaplaySDKPath>") {
				return false, nil
			}
			if !strings.Contains(buildPropsStr, "<SharedCodePath>") && !strings.Contains(buildPropsStr, "<GameLogicPath>") {
				return false, nil
			}

			return true, nil
		})
	}

	// Find game-specific dashboard directory.
	var gameDashboardPath string
	if o.flagGameDashboardPath != "" {
		gameDashboardPath = o.flagGameDashboardPath
	} else {
		gameDashboardPath, err = findSubDirectory("game-specific dashboard", o.absoluteProjectPath, func(rootPath, relPath string) (bool, error) {
			// Check for required files
			packageJSONPath := filepath.Join(rootPath, relPath, "package.json")
			tsconfigPath := filepath.Join(rootPath, relPath, "tsconfig.json")

			// Check if files exist
			if _, err := os.Stat(packageJSONPath); err != nil {
				return false, nil
			}
			if _, err := os.Stat(tsconfigPath); err != nil {
				return false, nil
			}

			// Read package.json content
			packageJSONContent, err := os.ReadFile(packageJSONPath)
			if err != nil {
				return false, nil
			}

			// Check for @metaplay/core dependency
			if !strings.Contains(string(packageJSONContent), `"@metaplay/core"`) {
				return false, nil
			}

			// Read tsconfig.json content
			tsconfigContent, err := os.ReadFile(tsconfigPath)
			if err != nil {
				return false, nil
			}

			// Check for MetaplaySDK reference
			if !strings.Contains(string(tsconfigContent), "MetaplaySDK") {
				return false, nil
			}

			return true, nil
		})
		// Note: ignore errors (game-specific dashboard is optional)
	}

	// Find Unity project directory.
	unityProjectPath, err := findUnityProjectPath(o.absoluteProjectPath)
	if err != nil {
		return nil, err
	}

	// Get shared code path from flag or parse from Directory.Build.props
	var sharedCodePath string
	if o.flagSharedCodePath != "" {
		sharedCodePath = o.flagSharedCodePath
	} else if gameBackendPath != "" {
		buildPropsPath := filepath.Join(o.absoluteProjectPath, gameBackendPath, "Directory.Build.props")
		buildPropsContent, err := os.ReadFile(buildPropsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read Directory.Build.props: %w", err)
		}

		// Look for SharedCodePath or GameLogicPath (used by older projects) using string
		// operations since it's a simple XML structure.
		// Example: <SharedCodePath>../SharedCode</SharedCodePath>
		// Example: <GameLogicPath>../GameLogic</GameLogicPath>
		content := string(buildPropsContent)

		// Try SharedCodePath first
		startTag := "<SharedCodePath>"
		endTag := "</SharedCodePath>"
		startIndex := strings.Index(content, startTag)
		endIndex := strings.Index(content, endTag)

		// If SharedCodePath not found, try GameLogicPath
		if startIndex == -1 || endIndex == -1 {
			startTag = "<GameLogicPath>"
			endTag = "</GameLogicPath>"
			startIndex = strings.Index(content, startTag)
			endIndex = strings.Index(content, endTag)

			if startIndex == -1 || endIndex == -1 {
				return nil, fmt.Errorf("neither SharedCodePath nor GameLogicPath found in Directory.Build.props")
			}
		}

		// Extract the path value between the tags
		sharedCodePath = content[startIndex+len(startTag) : endIndex]

		// Replace '$(MSBuildThisFileDirectory)' with the path of the file.
		sharedCodePath = strings.Replace(sharedCodePath, "$(MSBuildThisFileDirectory)", gameBackendPath+"/", -1)

		// Convert the path to be relative to the project root
		// The path in Directory.Build.props is relative to the backend directory
		sharedCodePath = filepath.Clean(sharedCodePath)
	}

	// Get .NET runtime version from flag or parse from global.json
	var dotnetRuntimeVersion string
	if o.flagDotnetRuntimeVer != "" {
		dotnetRuntimeVersion = o.flagDotnetRuntimeVer
	} else if gameBackendPath != "" {
		globalJSONPath := filepath.Join(o.absoluteProjectPath, gameBackendPath, "global.json")
		globalJSONContent, err := os.ReadFile(globalJSONPath)
		if err == nil {
			var globalJson struct {
				SDK struct {
					Version string `json:"version"`
				} `json:"sdk"`
			}
			if err := json.Unmarshal(globalJSONContent, &globalJson); err != nil {
				return nil, fmt.Errorf("failed to parse .NET runtime version from global.json")
			}
			parts := strings.Split(globalJson.SDK.Version, ".")
			if len(parts) < 2 {
				return nil, fmt.Errorf("invalid .NET runtime vesion in global.json")
			}
			// Only keep major.minor, e.g., '9.0'.
			dotnetRuntimeVersion = strings.Join(parts[0:2], ".")
		}
	}

	return &detectedProjectConfig{
		metaplaySdkPath:      metaplaySdkPath,
		gameBackendPath:      gameBackendPath,
		gameDashboardPath:    gameDashboardPath,
		unityProjectPath:     unityProjectPath,
		sharedCodePath:       sharedCodePath,
		dotnetRuntimeVersion: dotnetRuntimeVersion,
	}, nil
}
