/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
			return "", fmt.Errorf("provided path '%s' is not a file or directory", flagProjectConfigPath)
		}

		if info.IsDir() {
			// Check if the config file exists in the specified directory
			configFilePath := filepath.Join(flagProjectConfigPath, metaproj.ConfigFileName)
			if _, err := os.Stat(configFilePath); err == nil {
				return flagProjectConfigPath, nil
			}
			return "", fmt.Errorf("unable to find metaplay-project.yaml in directory '%s'", flagProjectConfigPath)
		} else {
			// Check if the specified file is the config file
			if filepath.Base(flagProjectConfigPath) == metaproj.ConfigFileName {
				return filepath.Dir(flagProjectConfigPath), nil
			}
			return "", errors.New("specified file is not metaplay-project.yaml")
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
			return "", errors.New("metaplay-project.yaml file not found in any parent directory, use --project=<path> to point to your project directory")
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
		return nil, fmt.Errorf("trying to resolve auth provider '%s'; project doesn't define any custom providers", providerName)
	}

	// Find the matching provider (by ID or name).
	for providerID, provider := range project.Config.AuthProviders {
		if providerID == providerName || provider.Name == providerName {
			return provider, nil
		}
	}

	// Provider not found, return an error.
	existingAuthProviders := []string{}
	for providerID, _ := range project.Config.AuthProviders {
		existingAuthProviders = append(existingAuthProviders, providerID)
	}
	return nil, fmt.Errorf("no matching auth provider '%s' found; project has the following providers: %v", providerName, existingAuthProviders)
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
			if tui.IsInteractiveMode() {
				envConfig, err = tui.ChooseTargetEnvironmentDialog(project.Config.Environments)
				if err != nil {
					return nil, nil, err
				}
			} else {
				return nil, nil, fmt.Errorf("in non-interactive mode, target environment must be explicitly specified")
			}
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
		project, err := tui.ChooseOrgAndProject(tokenSet)
		if err != nil {
			return nil, nil, err
		}

		// Fetch all environments of the project.
		environments, err := portalClient.FetchProjectEnvironments(project.UUID)
		if err != nil {
			return nil, nil, err
		}

		// Let the user choose from the environments.
		portalEnv, err = tui.ChooseEnvironmentDialog(environments)
		if err != nil {
			return nil, nil, err
		}

		log.Info().Msgf(" %s %s %s", styles.RenderSuccess("✓"), portalEnv.Name, styles.RenderMuted(fmt.Sprintf("[%s]", portalEnv.HumanID)))
	} else {
		// Check that the specified environment ID is a valid human ID.
		if err := metaproj.ValidateEnvironmentID(environment); err != nil {
			return nil, nil, fmt.Errorf("full environment ID must be specified when metaplay-project.yaml not found: %w", err)
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
