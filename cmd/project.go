package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type ProjectEnvironmentConfig struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	Stack      string `yaml:"stack"` // \todo StackApiBaseURL instead?
	ValuesFile string `yaml:"valuesFile"`
}

// Metaplay project config file, named `.metaplay.yaml`.
type ProjectConfig struct {
	ProjectSlug   string `yaml:"projectSlug"`   // The project's slug (as in the portal) -- \todo replace with humanId
	BuildRootDir  string `yaml:"buildRootDir"`  // Relative path to the docker build root directory
	SdkRootDir    string `yaml:"sdkRootDir"`    // Relative path to the MetaplaySDK directory
	BackendDir    string `yaml:"backendDir"`    // Relative path to the project-specific backend directory
	SharedCodeDir string `yaml:"sharedCodeDir"` // Relative path to the shared code directory

	HelmChartRepository string `yaml:"helmChartRepository"` // Helm chart repository to use (defaults to 'https://charts.metaplay.dev')
	HelmChartVersion    string `yaml:"helmChartVersion"`    // Version of the Helm chart to use (or 'latest-prerelease' for absolute latest)

	Environments []ProjectEnvironmentConfig `yaml:"environments"`
}

// Name of the Metaplay project config file.
const projectConfigFileName = ".metaplay.yaml"

// projectCmd represents the project command
var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Build, test, manage SDK for your project",
}

func init() {
	rootCmd.AddCommand(projectCmd)
}

// Locate the Metaplay project directory, i.e., where .metaplay.yaml is located.
// If flagProjectConfigPath is given, use it as the directory or project file path.
// Otherwise, try to locate the config file from the current directory.
// The (relative or absolute) path to the project directory is returned.
// \todo Does not handle case mismatches well, eg: -p ..\samples\idler breaks in docker build on Windows
func findProjectDirectory() (string, error) {
	// If the flag is provided, check if it's a valid directory or file path
	if flagProjectConfigPath != "" {
		log.Debug().Msgf("Finding for Metaplay project in path '%s'", flagProjectConfigPath)
		info, err := os.Stat(flagProjectConfigPath)
		if err != nil {
			return "", fmt.Errorf("provided path '%s' is not a file or directory", flagProjectConfigPath)
		}

		if info.IsDir() {
			// Check if the config file exists in the specified directory
			configFilePath := filepath.Join(flagProjectConfigPath, projectConfigFileName)
			if _, err := os.Stat(configFilePath); err == nil {
				return flagProjectConfigPath, nil
			}
			return "", fmt.Errorf("directory '%s' does not contain the .metaplay.yaml file", flagProjectConfigPath)
		} else {
			// Check if the specified file is the config file
			if filepath.Base(flagProjectConfigPath) == projectConfigFileName {
				return filepath.Dir(flagProjectConfigPath), nil
			}
			return "", errors.New("specified file is not .metaplay.yaml")
		}
	}

	// Check that .metaplay.yaml exists in this directory
	if _, err := os.Stat(projectConfigFileName); err != nil {
		return "", errors.New(".metaplay.yaml file not found in the current directory, use --project=<path> to point to your project's .metaplay.yaml")
	}

	return ".", nil
}

// Load the Metaplay project config file (.metaplay.yaml) from the project directory.
func loadProjectConfigFile(projectDir string) (*ProjectConfig, error) {
	// Check that the provided path points to a file or directory.
	info, err := os.Stat(projectDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("the provided project path '%s' is not a directory", projectDir)
	}

	// Build the full path to the config file in the directory.
	configFilePath := filepath.Join(projectDir, projectConfigFileName)

	// Read the file content.
	content, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, err
	}

	// Unmarshal the YAML content into the ProjectConfig struct.
	var projectConfig ProjectConfig
	err = yaml.Unmarshal(content, &projectConfig)
	if err != nil {
		return nil, err
	}

	// Validate the project config.
	err = validateProjectConfig(&projectConfig)

	return &projectConfig, nil
}

// Check that the provided project config is a valid one.
func validateProjectConfig(config *ProjectConfig) error {
	// \todo Add validations:
	// - check that paths are specified
	// - check that paths exist
	// - paths must be relative (not absolute)

	return nil
}

// Locate and load the project config file, based on the --project flag.
func resolveProjectConfig() (string, *ProjectConfig, error) {
	// Find the path with the project config file.
	projectDir, err := findProjectDirectory()
	if err != nil {
		return "", nil, err
	}
	log.Debug().Msgf("Project located in directory %s", projectDir)

	// Load the project config file
	projectConfig, err := loadProjectConfigFile(projectDir)
	return projectDir, projectConfig, err
}

func findEnvironmentConfig(projectConfig *ProjectConfig, envId string) (*ProjectEnvironmentConfig, error) {
	if projectConfig == nil {
		return nil, errors.New("projectConfig is nil")
	}

	for _, envConfig := range projectConfig.Environments {
		if envConfig.ID == envId {
			return &envConfig, nil
		}
	}

	return nil, fmt.Errorf("environment with the ID '%s' not found in project config", envId)
}
