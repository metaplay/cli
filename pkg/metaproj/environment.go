/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package metaproj

import (
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/rs/zerolog/log"
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
	Slug                string                    `yaml:"slug"`                          // Mutable slug of the environment, eg, 'develop'.
	HumanID             string                    `yaml:"humanId"`                       // Stable human ID of the environment. Also the Kubernetes namespace.
	Type                portalapi.EnvironmentType `yaml:"type"`                          // Type of the environment (eg, development, Staging, production).
	StackDomain         string                    `yaml:"stackDomain"`                   // Stack base domain (eg, 'p1.metaplay.io').
	ServerValuesFile    string                    `yaml:"serverValuesFile,omitempty"`    // Relative path (from metaplay-project.yaml) to the game server deployment Helm values file.
	BotClientValuesFile string                    `yaml:"botclientValuesFile,omitempty"` // Relative path (from metaplay-project.yaml) to the bot client deployment Helm values file.
}

// Get the Kubernetes namespace for this environment. Same as HumanID but
// using explicit getter for clarity.
func (envConfig *ProjectEnvironmentConfig) GetKubernetesNamespace() string {
	return envConfig.HumanID
}

// Convert the environment type (from portal) to an environment family (for C#).
func (envConfig *ProjectEnvironmentConfig) GetEnvironmentFamily() string {
	envFamily, found := environmentTypeToFamilyMapping[envConfig.Type]
	if !found {
		log.Panic().Msgf("Invalid EnvironmentType: %s", envConfig.Type)
	}
	return envFamily
}

// Get the environment-type specific runtime options file to include in Helm values.
func (envConfig *ProjectEnvironmentConfig) GetEnvironmentSpecificRuntimeOptionsFile() string {
	configFilePath, found := environmentTypeToRuntimeOptionsFileMapping[envConfig.Type]
	if !found {
		log.Panic().Msgf("Invalid EnvironmentType: %s", envConfig.Type)
	}
	return configFilePath
}
