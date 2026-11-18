/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"testing"

	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
)

// The gate used by 'database reset' and 'database import-archive': only development and staging
// skip the confirmation, so unknown types are treated as production.
func TestRequireProductionConfirmation(t *testing.T) {
	testCases := []struct {
		envType   portalapi.EnvironmentType
		confirmed bool
		wantErr   bool
	}{
		{portalapi.EnvironmentTypeDevelopment, false, false},
		{portalapi.EnvironmentTypeStaging, false, false},
		{portalapi.EnvironmentTypeProduction, false, true},
		{portalapi.EnvironmentTypeProduction, true, false},
		{"Prod", false, true},
		{"Prod", true, false},
		{"", false, true},
	}
	for _, tc := range testCases {
		envConfig := &metaproj.ProjectEnvironmentConfig{Name: "Test", Type: tc.envType}
		err := requireProductionConfirmation(envConfig, tc.confirmed)
		if gotErr := err != nil; gotErr != tc.wantErr {
			t.Errorf("type %q, confirmed %v: got error %v, want error: %v", tc.envType, tc.confirmed, err, tc.wantErr)
		}
	}
}
