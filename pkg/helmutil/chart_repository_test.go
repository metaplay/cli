/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeChart writes a chart directory holding only a Chart.yaml with the given
// name, which is all the validation reads.
func writeChart(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	chartYaml := "apiVersion: v2\nname: " + name + "\nversion: 0.0.0-test\n"
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartYaml), 0o644); err != nil {
		t.Fatalf("failed to write Chart.yaml: %v", err)
	}
	return dir
}

// Each deploy command installs one chart, and a local copy of that chart is
// what --local-chart-path is for. A chart of the other kind would install
// cleanly and then do something else entirely, so it is refused, naming both.
func TestValidateLocalHelmChart(t *testing.T) {
	tests := []struct {
		name     string
		chart    string
		expected string
		valid    bool
	}{
		{"the game server chart, for deploy server", "metaplay-gameserver", "metaplay-gameserver", true},
		{"the loadtest chart, for deploy botclient", "metaplay-loadtest", "metaplay-loadtest", true},
		{"the loadtest chart, where the game server chart is expected", "metaplay-loadtest", "metaplay-gameserver", false},
		{"the game server chart, where the loadtest chart is expected", "metaplay-gameserver", "metaplay-loadtest", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateLocalHelmChart(writeChart(t, test.chart), test.expected)
			if test.valid {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected %s to be refused where %s is expected", test.chart, test.expected)
			}
			for _, named := range []string{test.chart, test.expected} {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("error %q does not name %s", err, named)
				}
			}
		})
	}
}
