/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"slices"
	"testing"
)

func TestSelectIntegrationTests(t *testing.T) {
	tests := []struct {
		name                string
		requested           []string
		usesCustomDashboard bool
		want                []string
		wantSkipped         bool
		wantErr             bool
	}{
		{"all with custom dashboard", nil, true, []string{"bots", "dashboard", "system"}, false, false},
		{"all without custom dashboard", nil, false, []string{"bots", "system"}, true, false},
		{"one", []string{"bots"}, true, []string{"bots"}, false, false},
		{"several in fixed order", []string{"system", "bots"}, true, []string{"bots", "system"}, false, false},
		{"bots and dashboard", []string{"bots", "dashboard"}, true, []string{"bots", "dashboard"}, false, false},
		{"requested dashboard without custom dashboard", []string{"bots", "dashboard"}, false, nil, false, true},
		{"unrequested dashboard is not skipped", []string{"bots"}, false, []string{"bots"}, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selected, skipped, err := selectIntegrationTests(test.requested, test.usesCustomDashboard)
			if (err != nil) != test.wantErr {
				t.Fatalf("err = %v, want error: %v", err, test.wantErr)
			}
			var names []string
			for _, s := range selected {
				names = append(names, s.name)
			}
			if !slices.Equal(names, test.want) {
				t.Errorf("tests = %v, want %v", names, test.want)
			}
			if skipped != test.wantSkipped {
				t.Errorf("dashboardSkipped = %v, want %v", skipped, test.wantSkipped)
			}
		})
	}
}
