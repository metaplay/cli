/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	clierrors "github.com/metaplay/cli/internal/errors"
)

// writeLocalChart writes a chart directory holding only a Chart.yaml with the
// given name, which is all the validation reads.
func writeLocalChart(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	chartYaml := "apiVersion: v2\nname: " + name + "\nversion: 0.0.0-test\n"
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartYaml), 0o644); err != nil {
		t.Fatalf("failed to write Chart.yaml: %v", err)
	}
	return dir
}

// Each deploy command takes a local copy of its own chart in Prepare, and
// refuses the other's there, before Run resolves the environment, which may ask
// to log in first. The refusal is a usage error that names the flag.
func TestDeploy_PrepareTakesOnlyTheCommandsOwnLocalChart(t *testing.T) {
	gameServerChart := writeLocalChart(t, metaplayGameServerChartName)
	loadTestChart := writeLocalChart(t, metaplayLoadTestChartName)

	commands := []struct {
		name       string
		prepare    func(localChartPath string) error
		ownChart   string
		otherChart string
	}{
		{
			name: "deploy server",
			prepare: func(localChartPath string) error {
				o := deployGameServerOpts{flagHelmChartLocalPath: localChartPath}
				return o.Prepare(nil, nil)
			},
			ownChart:   gameServerChart,
			otherChart: loadTestChart,
		},
		{
			name: "deploy botclient",
			prepare: func(localChartPath string) error {
				o := deployBotClientOpts{argImageTag: "20260601-153000-1a27c25", flagHelmChartLocalPath: localChartPath}
				return o.Prepare(nil, nil)
			},
			ownChart:   loadTestChart,
			otherChart: gameServerChart,
		},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			if err := command.prepare(""); err != nil {
				t.Errorf("no local chart refused: %v", err)
			}
			if err := command.prepare(command.ownChart); err != nil {
				t.Errorf("own chart refused: %v", err)
			}

			err := command.prepare(command.otherChart)
			if err == nil {
				t.Fatal("the other command's chart was accepted")
			}
			if !clierrors.IsUsageError(err) {
				t.Errorf("error = %v, want a usage error", err)
			}
			if !strings.Contains(err.Error(), "--local-chart-path") {
				t.Errorf("error %q does not name --local-chart-path", err)
			}
		})
	}
}
