/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// Locate the Metaplay project directory, i.e., where metaplay-project.yaml is located.
// If flagProjectConfigPath is given, use it as the directory or project file path.
// Otherwise, try to locate the config file from the current directory.
// The (relative or absolute) path to the project directory is returned.
// \todo Does not handle case mismatches well, eg: -p ..\samples\idler breaks in docker build on Windows
func findProjectDirectory() (string, error) {
	// If the flag is provided, check if it's a valid directory or file path
	if flagProjectConfigPath != "" {
		log.Debug().Msgf("Try to locate Metaplay project in path '%s'", flagProjectConfigPath)
		info, err := os.Stat(flagProjectConfigPath)
		if err != nil {
			return "", clierrors.Newf("Path '%s' does not exist", flagProjectConfigPath).
				WithCause(err).
				WithSuggestion("Check that the path is correct")
		}

		if info.IsDir() {
			// Check if the config file exists in the specified directory
			configFilePath := filepath.Join(flagProjectConfigPath, metaproj.ConfigFileName)
			if _, err := os.Stat(configFilePath); err == nil {
				return flagProjectConfigPath, nil
			}
			return "", clierrors.Newf("No metaplay-project.yaml found in '%s'", flagProjectConfigPath).
				WithSuggestion("Run 'metaplay init project' to create one, or specify a different directory with --project")
		} else {
			// Check if the specified file is the config file
			if filepath.Base(flagProjectConfigPath) == metaproj.ConfigFileName {
				return filepath.Dir(flagProjectConfigPath), nil
			}
			return "", clierrors.New("Specified file is not metaplay-project.yaml").
				WithSuggestion("Use --project to specify the directory containing metaplay-project.yaml")
		}
	}

	// Start from the current directory and walk up towards the root
	// until we find metaplay-project.yaml
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	// Convert to absolute path to handle relative paths correctly
	absCurrentDir, err := filepath.Abs(currentDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Walk up the directory tree to find the metaplay-project.yaml
	for {
		// Check if the config file exists in the current directory
		configFilePath := filepath.Join(absCurrentDir, metaproj.ConfigFileName)
		if _, err := os.Stat(configFilePath); err == nil {
			// Found the config file, return the directory
			log.Debug().Msgf("Found metaplay-project.yaml in directory '%s'", absCurrentDir)

			// Return path relative to the starting directory if possible
			relPath, err := filepath.Rel(currentDir, absCurrentDir)
			if err == nil && !filepath.IsAbs(relPath) {
				return relPath, nil
			}
			return absCurrentDir, nil
		}

		// Get the parent directory
		parentDir := filepath.Dir(absCurrentDir)

		// Check if we've reached the root directory
		if parentDir == absCurrentDir {
			// We've reached the root and didn't find the config file
			return "", clierrors.New("Cannot find metaplay-project.yaml").
				WithSuggestion("Make sure you are in the right directory, or use --project=<path> to specify the project directory")
		}

		// Move up to the parent directory
		absCurrentDir = parentDir
	}
}

// Get the AuthProvider: either return the project's custom provider (if defined),
// or otherwise use the default Metaplay Auth.
func getAuthProvider(project *metaproj.MetaplayProject, providerName string) (*auth.AuthProviderConfig, error) {
	if providerName == "" || providerName == "metaplay" {
		log.Debug().Msgf("Using built-in provider 'metaplay'")
		return auth.NewMetaplayAuthProvider(), nil
	} else {
		log.Debug().Msgf("Resolving auth provider '%s'", providerName)
	}

	// If have a project, return its auth provider.
	if project.Config.AuthProviders == nil {
		return nil, clierrors.Newf("Auth provider '%s' not found", providerName).
			WithDetails("Project doesn't define any custom auth providers").
			WithSuggestion("Use the default 'metaplay' provider, or add custom providers to metaplay-project.yaml")
	}

	// Find the matching provider (by ID or name).
	for providerID, provider := range project.Config.AuthProviders {
		if providerID == providerName || provider.Name == providerName {
			return provider, nil
		}
	}

	// Provider not found, return an error.
	existingAuthProviders := []string{}
	for providerID := range project.Config.AuthProviders {
		existingAuthProviders = append(existingAuthProviders, providerID)
	}
	return nil, clierrors.Newf("Auth provider '%s' not found", providerName).
		WithDetails(fmt.Sprintf("Available providers: %v", existingAuthProviders))
}

// Load the metaplay-project.yaml from the specified directory.
func loadProject(projectDir string) (*metaproj.MetaplayProject, error) {
	// Load the project config file.
	projectConfig, err := metaproj.LoadProjectConfigFile(projectDir)
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("Project config loaded: %#v", projectConfig)

	// Load version metadata from MetaplaySDK/version.yaml.
	versionMetadata, err := metaproj.LoadSdkVersionMetadata(filepath.Join(projectDir, projectConfig.SdkRootDir))
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("Version metadata loaded: %+v", versionMetadata)

	return metaproj.NewMetaplayProject(projectDir, projectConfig, versionMetadata)
}

// Try to find the metaplay-project.yaml based on the --project flag, and load
// it if found. Returns nil, nil if not found.
func tryResolveProject() (*metaproj.MetaplayProject, error) {
	// Check if we can find the project file.
	projectDir, err := findProjectDirectory()
	if err != nil {
		return nil, nil
	}

	// If found the project file, load it.
	return loadProject(projectDir)
}

// Locate and load the project config file, based on the --project flag.
func resolveProject() (*metaproj.MetaplayProject, error) {
	// Find the path with the project config file.
	projectDir, err := findProjectDirectory()
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("Project located in directory %s", projectDir)

	return loadProject(projectDir)
}

// Resolve the environment configuration. First, try the project config, if available.
// Otherwise, fetch the information from the portal.
func resolveEnvironment(ctx context.Context, project *metaproj.MetaplayProject, environment string) (*metaproj.ProjectEnvironmentConfig, *auth.TokenSet, error) {
	var envConfig *metaproj.ProjectEnvironmentConfig
	var err error

	// If a metaplay-project.yaml can be located, resolve the environment
	// from the project config.
	if project != nil {
		// If environment not specified, ask it from the user (if in interactive mode).
		if environment == "" {
			// Must be in interactive mode.
			if !tui.IsInteractiveMode() {
				return nil, nil, clierrors.NewUsageError("Target environment must be specified in non-interactive mode").
					WithSuggestion("Provide the environment name as an argument, e.g., 'metaplay <command> develop'")
			}

			// Error if no environments in the metaplay-project.yaml.
			if len(project.Config.Environments) == 0 {
				return nil, nil, clierrors.New("No environments found in metaplay-project.yaml").
					WithSuggestion("Run 'metaplay update project-environments' to sync from portal, or create one at https://portal.metaplay.dev")
			}

			// Let the user choose the target environment.
			envConfig, err = tui.ChooseFromListDialog(
				"Select Target Environment",
				project.Config.Environments,
				func(env *metaproj.ProjectEnvironmentConfig) (string, string) {
					return env.Name, fmt.Sprintf("[%s]", env.HumanID)
				},
			)
			if err != nil {
				return nil, nil, err
			}

			log.Info().Msgf(" %s %s %s", styles.RenderSuccess("✓"), envConfig.Name, styles.RenderMuted(fmt.Sprintf("[%s]", envConfig.HumanID)))
		} else {
			// Find target environment.
			envConfig, err = project.Config.FindEnvironmentConfig(environment)
			if err != nil {
				return nil, nil, err
			}
		}

		// Get auth provider for env.
		authProvider, err := getAuthProvider(project, envConfig.AuthProvider)
		if err != nil {
			return nil, nil, err
		}

		// Ensure the user is logged in.
		tokenSet, err := tui.RequireLoggedIn(ctx, authProvider)
		if err != nil {
			return nil, nil, err
		}

		return envConfig, tokenSet, nil
	}

	// If no metaplay-project.yaml can be located, we know we are using Metaplay auth provider.
	// \todo store in project config instead?
	authProvider := auth.NewMetaplayAuthProvider()

	// Ensure the user is logged in.
	tokenSet, err := tui.RequireLoggedIn(ctx, authProvider)
	if err != nil {
		return nil, nil, err
	}

	// If target environment not specified, let user choose from all accessible portal projects
	// and then the project's environments.
	var portalEnv *portalapi.EnvironmentInfo
	portalClient := portalapi.NewClient(tokenSet)
	if environment == "" {
		// Let the user choose from the accessible ones.
		project, err := chooseOrgAndProject(portalClient, "")
		if err != nil {
			return nil, nil, err
		}

		// Fetch all environments of the project.
		environments, err := portalClient.FetchProjectEnvironments(project.UUID)
		if err != nil {
			return nil, nil, err
		}

		// Must be in interactive mode.
		if !tui.IsInteractiveMode() {
			return nil, nil, clierrors.NewUsageError("Interactive mode required for project selection").
				WithSuggestion("Specify the environment explicitly, or run in an interactive terminal")
		}

		// Error if no environments in portal project.
		if len(environments) == 0 {
			return nil, nil, clierrors.Newf("No accessible environments found for project '%s'", project.Name).
				WithSuggestion("Create an environment at https://portal.metaplay.dev or request access from your team")
		}

		// Let user interactively choose the environment.
		portalEnv, err := tui.ChooseFromListDialog[portalapi.EnvironmentInfo](
			"Select Target Environment",
			environments,
			func(env *portalapi.EnvironmentInfo) (string, string) {
				return env.Name, fmt.Sprintf("[%s]", env.HumanID)
			},
		)
		if err != nil {
			return nil, nil, err
		}

		log.Info().Msgf(" %s %s %s", styles.RenderSuccess("✓"), portalEnv.Name, styles.RenderMuted(fmt.Sprintf("[%s]", portalEnv.HumanID)))
	} else {
		// Check that the specified environment ID is a valid human ID.
		if err := metaproj.ValidateEnvironmentID(portalapi.HostingTypeMetaplayHosted, environment); err != nil {
			return nil, nil, clierrors.WrapUsageError(err, "Invalid environment ID format").
				WithSuggestion("Use the full environment ID (e.g., 'tough-falcons') when metaplay-project.yaml is not available")
		}

		// Try to resolve the environment from the portal by its human ID.
		var err error
		portalEnv, err = portalClient.FetchEnvironmentInfoByHumanID(environment)
		if err != nil {
			return nil, nil, err
		}
	}

	// Convert to ProjectEnvironmentConfig.
	envConfig = &metaproj.ProjectEnvironmentConfig{
		Name:         portalEnv.Name,
		HumanID:      portalEnv.HumanID,
		StackDomain:  portalEnv.StackDomain,
		Type:         portalEnv.Type,
		AuthProvider: "metaplay",
	}
	return envConfig, tokenSet, nil
}

// Helper for resolving both the MetaplayProject and a specific environment at the same time.
// This operation is common enough to justify its own method.
func resolveProjectAndEnvironment(environment string) (*metaproj.MetaplayProject, *metaproj.ProjectEnvironmentConfig, error) {
	// Resolve the project.
	project, err := resolveProject()
	if err != nil {
		return nil, nil, err
	}

	// Find target environment.
	envConfig, err := project.Config.FindEnvironmentConfig(environment)
	if err != nil {
		return nil, nil, err
	}

	return project, envConfig, nil
}

// Choose target project either with human ID provided (still validated against the portal-returned data) or
// let the user interactively choose from a list of projects fetched from the portal.
func chooseOrgAndProject(portalClient *portalapi.Client, projectID string) (*portalapi.ProjectInfo, error) {
	// Fetch all the available organizations and projects.
	orgsAndProjects, err := portalClient.FetchUserOrgsAndProjects()
	if err != nil {
		return nil, err
	}

	// If projectID is specified, find it from the list (or error out).
	if projectID != "" {
		var foundProject *portalapi.ProjectInfo
		for _, org := range orgsAndProjects {
			for _, project := range org.Projects {
				if project.HumanID == projectID {
					foundProject = &project
					break
				}
			}
			if foundProject != nil {
				break
			}
		}

		if foundProject == nil {
			return nil, clierrors.Newf("Project '%s' not found", projectID).
				WithSuggestion("Check the project ID and ensure you have access to it at https://portal.metaplay.dev")
		}

		return foundProject, nil
	} else {
		// Otherwise, let the user choose interactively.
		return tui.ChooseOrgAndProject(orgsAndProjects)
	}
}
