/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	clierrors "github.com/metaplay/cli/internal/errors"
)

// --helm-chart-repo overrides the project config's repository, which
// overrides Metaplay's own.
func TestHelmChartRepository_FlagOverridesTheProjectConfig(t *testing.T) {
	for _, tc := range []struct {
		name             string
		flagRepository   string
		configRepository string
		want             string
	}{
		{"flag and config", "https://flag.example", "https://config.example", "https://flag.example"},
		{"config only", "", "https://config.example", "https://config.example"},
		{"flag only", "https://flag.example", "", "https://flag.example"},
		{"neither", "", "", "https://charts.metaplay.dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := helmChartRepository(tc.flagRepository, tc.configRepository); got != tc.want {
				t.Errorf("helmChartRepository(%q, %q) = %q, want %q", tc.flagRepository, tc.configRepository, got, tc.want)
			}
		})
	}
}

// --values replaces the environment's values file from the project config,
// rather than adding to it.
func TestHelmValuesFiles_FlagReplacesTheProjectConfigs(t *testing.T) {
	configValuesFiles := []string{"Backend/Deployments/develop-server.yaml"}

	if got := helmValuesFiles("custom.yaml", configValuesFiles); !slices.Equal(got, []string{"custom.yaml"}) {
		t.Errorf("with --values: %v, want only the flag's file", got)
	}
	if got := helmValuesFiles("", configValuesFiles); !slices.Equal(got, configValuesFiles) {
		t.Errorf("without --values: %v, want the project config's %v", got, configValuesFiles)
	}
}

// Each deploy command refuses a --values that is not a file in Prepare, before
// Run resolves the environment, which may ask to log in first. The refusal is
// a usage error that names the flag.
func TestDeploy_PrepareRefusesAValuesFileThatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	valuesFile := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(valuesFile, []byte("replicas: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	commands := []struct {
		name    string
		prepare func(valuesPath string) error
	}{
		{
			name: "deploy server",
			prepare: func(valuesPath string) error {
				o := deployGameServerOpts{flagHelmValuesPath: valuesPath}
				return o.Prepare(nil, nil)
			},
		},
		{
			name: "deploy botclient",
			prepare: func(valuesPath string) error {
				o := deployBotClientOpts{argImageTag: "20260601-153000-1a27c25", flagHelmValuesPath: valuesPath}
				return o.Prepare(nil, nil)
			},
		},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			if err := command.prepare(""); err != nil {
				t.Errorf("no values file refused: %v", err)
			}
			if err := command.prepare(valuesFile); err != nil {
				t.Errorf("values file refused: %v", err)
			}

			for _, refused := range []string{filepath.Join(dir, "missing.yaml"), dir} {
				err := command.prepare(refused)
				if err == nil {
					t.Errorf("--values %s was accepted", refused)
					continue
				}
				if !clierrors.IsUsageError(err) {
					t.Errorf("--values %s: error = %v, want a usage error", refused, err)
				}
				if !strings.Contains(err.Error(), "--values") {
					t.Errorf("--values %s: error %q does not name --values", refused, err)
				}
			}
		})
	}
}
