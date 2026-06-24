/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// DatabaseRuntimeOptions holds the subset of the `Database` runtime options section that the CLI
// reads from the project's runtime options YAML files. Fields are nil when not configured.
type DatabaseRuntimeOptions struct {
	MasterVersion         *int  `yaml:"MasterVersion"`
	NukeOnVersionMismatch *bool `yaml:"NukeOnVersionMismatch"`
}

// runtimeOptionsFile mirrors the relevant sections of a runtime options YAML file.
type runtimeOptionsFile struct {
	Database *DatabaseRuntimeOptions `yaml:"Database"`
}

// ReadDatabaseRuntimeOptions reads the `Database` section of the project's runtime options for the
// given environment. It merges Backend/Server/Config/Options.base.yaml with the environment-specific
// options file (e.g. Options.dev.yaml), with the environment-specific file taking precedence. Missing
// files are skipped. Returns the merged options, with fields left nil when not configured.
//
// Note: option keys are matched case-sensitively against their canonical PascalCase names (e.g.
// `Database:MasterVersion`), matching the Metaplay-generated options files.
func (project *MetaplayProject) ReadDatabaseRuntimeOptions(envConfig *ProjectEnvironmentConfig) (*DatabaseRuntimeOptions, error) {
	serverDir := project.GetServerDir()
	paths := []string{filepath.Join(serverDir, "Config", "Options.base.yaml")}

	// Add the environment-specific options file. Look the mapping up directly (rather than via
	// GetEnvironmentSpecificRuntimeOptionsFile, which panics on an unknown type) so an unexpected
	// environment type degrades to reading only the base file instead of crashing this best-effort read.
	if envFile, ok := environmentTypeToRuntimeOptionsFileMapping[envConfig.Type]; ok {
		paths = append(paths, filepath.Join(serverDir, envFile))
	} else {
		log.Debug().Msgf("Unknown environment type '%s'; reading only Options.base.yaml", envConfig.Type)
	}

	merged := &DatabaseRuntimeOptions{}
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			// Missing options files are not an error: the environment may simply rely on defaults.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("failed to read runtime options file %s: %w", path, err)
		}

		var parsed runtimeOptionsFile
		if err := yaml.Unmarshal(content, &parsed); err != nil {
			return nil, fmt.Errorf("failed to parse runtime options file %s: %w", path, err)
		}

		// Later files override earlier ones, but only for fields they actually specify.
		if parsed.Database != nil {
			if parsed.Database.MasterVersion != nil {
				merged.MasterVersion = parsed.Database.MasterVersion
			}
			if parsed.Database.NukeOnVersionMismatch != nil {
				merged.NukeOnVersionMismatch = parsed.Database.NukeOnVersionMismatch
			}
		}
	}

	return merged, nil
}
