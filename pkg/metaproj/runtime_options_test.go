/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/metaplay/cli/pkg/portalapi"
)

// writeOptionsFile writes a runtime options YAML file into <serverDir>/Config/<name>.
func writeOptionsFile(t *testing.T, serverDir, name, content string) {
	t.Helper()
	configDir := filepath.Join(serverDir, "Config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, name), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", name, err)
	}
}

func newTestProject(rootDir string) *MetaplayProject {
	return &MetaplayProject{
		RelativeDir: rootDir,
		Config:      ProjectConfig{BackendDir: "Backend"},
	}
}

func TestReadDatabaseRuntimeOptions_EnvOverridesBase(t *testing.T) {
	root := t.TempDir()
	project := newTestProject(root)
	serverDir := project.GetServerDir()

	// Base sets both fields; the dev file overrides only MasterVersion.
	writeOptionsFile(t, serverDir, "Options.base.yaml", "Database:\n  MasterVersion: 5\n  NukeOnVersionMismatch: false\n")
	writeOptionsFile(t, serverDir, "Options.dev.yaml", "Database:\n  MasterVersion: 7\n")

	envConfig := &ProjectEnvironmentConfig{Type: portalapi.EnvironmentTypeDevelopment}
	opts, err := project.ReadDatabaseRuntimeOptions(envConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.MasterVersion == nil || *opts.MasterVersion != 7 {
		t.Errorf("MasterVersion = %v, want 7 (dev overrides base)", opts.MasterVersion)
	}
	if opts.NukeOnVersionMismatch == nil || *opts.NukeOnVersionMismatch != false {
		t.Errorf("NukeOnVersionMismatch = %v, want false (inherited from base)", opts.NukeOnVersionMismatch)
	}
}

func TestReadDatabaseRuntimeOptions_MissingFiles(t *testing.T) {
	root := t.TempDir()
	project := newTestProject(root)

	// No options files exist at all; should return empty options without error.
	envConfig := &ProjectEnvironmentConfig{Type: portalapi.EnvironmentTypeProduction}
	opts, err := project.ReadDatabaseRuntimeOptions(envConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.MasterVersion != nil {
		t.Errorf("MasterVersion = %v, want nil", opts.MasterVersion)
	}
	if opts.NukeOnVersionMismatch != nil {
		t.Errorf("NukeOnVersionMismatch = %v, want nil", opts.NukeOnVersionMismatch)
	}
}

func TestReadDatabaseRuntimeOptions_BaseOnly(t *testing.T) {
	root := t.TempDir()
	project := newTestProject(root)
	serverDir := project.GetServerDir()

	// Only the base file is present (no env-specific file); base values apply.
	writeOptionsFile(t, serverDir, "Options.base.yaml", "Database:\n  MasterVersion: 3\n")

	envConfig := &ProjectEnvironmentConfig{Type: portalapi.EnvironmentTypeStaging}
	opts, err := project.ReadDatabaseRuntimeOptions(envConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.MasterVersion == nil || *opts.MasterVersion != 3 {
		t.Errorf("MasterVersion = %v, want 3", opts.MasterVersion)
	}
	if opts.NukeOnVersionMismatch != nil {
		t.Errorf("NukeOnVersionMismatch = %v, want nil", opts.NukeOnVersionMismatch)
	}
}
