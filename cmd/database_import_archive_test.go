/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"strings"
	"testing"

	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
)

func TestDetectMasterVersionMismatch(t *testing.T) {
	tests := []struct {
		name           string
		archiveMV      *int
		dbOpts         *metaproj.DatabaseRuntimeOptions
		envType        portalapi.EnvironmentType
		wantNil        bool
		wantDevDefault bool
		wantArchiveMV  int
		wantProjectMV  int
	}{
		{
			name:           "dev env mismatch warns (dev default)",
			archiveMV:      new(3),
			dbOpts:         &metaproj.DatabaseRuntimeOptions{MasterVersion: new(5)},
			envType:        portalapi.EnvironmentTypeDevelopment,
			wantNil:        false,
			wantDevDefault: true,
			wantArchiveMV:  3,
			wantProjectMV:  5,
		},
		{
			name:      "dev env matching versions does not warn",
			archiveMV: new(5),
			dbOpts:    &metaproj.DatabaseRuntimeOptions{MasterVersion: new(5)},
			envType:   portalapi.EnvironmentTypeDevelopment,
			wantNil:   true,
		},
		{
			name:      "production env mismatch does not warn (no nuke by default)",
			archiveMV: new(3),
			dbOpts:    &metaproj.DatabaseRuntimeOptions{MasterVersion: new(5)},
			envType:   portalapi.EnvironmentTypeProduction,
			wantNil:   true,
		},
		{
			name:           "production env mismatch warns when nuke explicitly enabled",
			archiveMV:      new(3),
			dbOpts:         &metaproj.DatabaseRuntimeOptions{MasterVersion: new(5), NukeOnVersionMismatch: new(true)},
			envType:        portalapi.EnvironmentTypeProduction,
			wantNil:        false,
			wantDevDefault: false,
			wantArchiveMV:  3,
			wantProjectMV:  5,
		},
		{
			name:      "dev env mismatch does not warn when nuke explicitly disabled",
			archiveMV: new(3),
			dbOpts:    &metaproj.DatabaseRuntimeOptions{MasterVersion: new(5), NukeOnVersionMismatch: new(false)},
			envType:   portalapi.EnvironmentTypeDevelopment,
			wantNil:   true,
		},
		{
			name:      "missing archive master version does not warn",
			archiveMV: nil,
			dbOpts:    &metaproj.DatabaseRuntimeOptions{MasterVersion: new(5)},
			envType:   portalapi.EnvironmentTypeDevelopment,
			wantNil:   true,
		},
		{
			name:      "missing project master version does not warn",
			archiveMV: new(3),
			dbOpts:    &metaproj.DatabaseRuntimeOptions{},
			envType:   portalapi.EnvironmentTypeDevelopment,
			wantNil:   true,
		},
		{
			name:      "nil db options does not warn",
			archiveMV: new(3),
			dbOpts:    nil,
			envType:   portalapi.EnvironmentTypeDevelopment,
			wantNil:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectMasterVersionMismatch(tc.archiveMV, tc.dbOpts, tc.envType)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected no mismatch, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected a mismatch, got nil")
			}
			if got.ArchiveMasterVersion != tc.wantArchiveMV {
				t.Errorf("ArchiveMasterVersion = %d, want %d", got.ArchiveMasterVersion, tc.wantArchiveMV)
			}
			if got.ProjectMasterVersion != tc.wantProjectMV {
				t.Errorf("ProjectMasterVersion = %d, want %d", got.ProjectMasterVersion, tc.wantProjectMV)
			}
			if got.NukeIsDevDefault != tc.wantDevDefault {
				t.Errorf("NukeIsDevDefault = %v, want %v", got.NukeIsDevDefault, tc.wantDevDefault)
			}
		})
	}
}

func TestMasterVersionMismatchWarningLine(t *testing.T) {
	devWarning := (&masterVersionMismatch{ArchiveMasterVersion: 3, ProjectMasterVersion: 5, NukeIsDevDefault: true}).warningLine()
	if !strings.Contains(devWarning, "MasterVersion 3") || !strings.Contains(devWarning, "deploys 5") {
		t.Errorf("warning should mention both versions, got: %q", devWarning)
	}
	if !strings.Contains(devWarning, "A development env resets") {
		t.Errorf("dev-default warning should mention development env, got: %q", devWarning)
	}

	explicitWarning := (&masterVersionMismatch{ArchiveMasterVersion: 3, ProjectMasterVersion: 5, NukeIsDevDefault: false}).warningLine()
	if !strings.Contains(explicitWarning, "This environment is configured to reset") {
		t.Errorf("explicit warning should mention configured reset, got: %q", explicitWarning)
	}
}
