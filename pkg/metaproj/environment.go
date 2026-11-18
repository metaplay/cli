/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
	"fmt"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/portalapi"
)

// Environment family (given to Helm / C# server).
type EnvironmentFamily string

const (
	EnvironmentFamilyDevelopment = "Development"
	EnvironmentFamilyStaging     = "Staging"
	EnvironmentFamilyProduction  = "Production"
)

// Map EnvironmentType (from portal) to an EnvironmentFamily (compatible with Helm and C#)
var environmentTypeToFamilyMapping = map[portalapi.EnvironmentType]string{
	portalapi.EnvironmentTypeDevelopment: EnvironmentFamilyDevelopment,
	portalapi.EnvironmentTypeStaging:     EnvironmentFamilyStaging,
	portalapi.EnvironmentTypeProduction:  EnvironmentFamilyProduction,
}

// Mapping from EnvironmentType to the runtime options file to include (in addition to Options.base.yaml).
var environmentTypeToRuntimeOptionsFileMapping = map[portalapi.EnvironmentType]string{
	portalapi.EnvironmentTypeDevelopment: "./Config/Options.dev.yaml",
	portalapi.EnvironmentTypeStaging:     "./Config/Options.staging.yaml",
	portalapi.EnvironmentTypeProduction:  "./Config/Options.production.yaml",
}

// Per-environment configuration from 'metaplay-project.yaml'.
// Note: When adding new fields, remember to update ValidateProjectConfig().
type ProjectEnvironmentConfig struct {
	Name                string                    `yaml:"name"`                          // Name of the environment.
	HostingType         portalapi.HostingType     `yaml:"hostingType"`                   // Type of hosting for the environment (Metaplay-hosted vs self-hosted)
	HumanID             string                    `yaml:"humanId"`                       // Stable human ID of the environment. Also the Kubernetes namespace.
	Type                portalapi.EnvironmentType `yaml:"type"`                          // Type of the environment (development, staging or production).
	StackDomain         string                    `yaml:"stackDomain"`                   // Stack base domain (eg, 'p1.metaplay.io').
	ServerValuesFile    string                    `yaml:"serverValuesFile,omitempty"`    // Relative path (from metaplay-project.yaml) to the game server deployment Helm values file.
	BotClientValuesFile string                    `yaml:"botclientValuesFile,omitempty"` // Relative path (from metaplay-project.yaml) to the bot client deployment Helm values file.
	AuthProvider        string                    `yaml:"authProvider,omitempty"`        // Name of the auth provider to use for this environment. Defaults to 'metaplay'.
	Aliases             []string                  `yaml:"aliases,omitempty"`             // Short aliases for the environment, e.g., 'dev', 'prod'.
}

// normalizeEnvironmentType lowercases and trims a hand-typed type such as 'Production'. It
// returns a warning when the value is not canonical. Unknown values are returned unchanged.
func normalizeEnvironmentType(envName string, envType portalapi.EnvironmentType) (portalapi.EnvironmentType, string) {
	trimmed := strings.TrimSpace(string(envType))
	if trimmed == "" {
		return "", "" // reported as missing by ValidateProjectConfig
	}
	normalized := portalapi.EnvironmentType(strings.ToLower(trimmed))
	if !normalized.IsKnown() {
		return envType, fmt.Sprintf("Environment '%s' in metaplay-project.yaml has unknown 'type: %s'. Use development, staging or production.", envName, envType)
	}
	if normalized != envType {
		return normalized, fmt.Sprintf("Environment '%s' in metaplay-project.yaml has 'type: %s'. Treating it as '%s'. Write it in lowercase to remove this warning.", envName, envType, normalized)
	}
	return normalized, ""
}

// unknownEnvironmentTypeError is returned for a type that has no family or runtime options file.
func unknownEnvironmentTypeError(envConfig *ProjectEnvironmentConfig) error {
	return clierrors.Newf("Environment '%s' has unknown type '%s'", envConfig.Name, envConfig.Type).
		WithSuggestion("Set 'type' to development, staging or production in metaplay-project.yaml")
}

// Get the Kubernetes namespace for this environment. Same as HumanID but
// using explicit getter for clarity.
func (envConfig *ProjectEnvironmentConfig) GetKubernetesNamespace() string {
	return envConfig.HumanID
}

// Convert the environment type (from portal) to an environment family (for C#).
func (envConfig *ProjectEnvironmentConfig) GetEnvironmentFamily() (string, error) {
	envFamily, found := environmentTypeToFamilyMapping[envConfig.Type]
	if !found {
		return "", unknownEnvironmentTypeError(envConfig)
	}
	return envFamily, nil
}

// Get the environment-type specific runtime options file to include in Helm values.
func (envConfig *ProjectEnvironmentConfig) GetEnvironmentSpecificRuntimeOptionsFile() (string, error) {
	configFilePath, found := environmentTypeToRuntimeOptionsFileMapping[envConfig.Type]
	if !found {
		return "", unknownEnvironmentTypeError(envConfig)
	}
	return configFilePath, nil
}
