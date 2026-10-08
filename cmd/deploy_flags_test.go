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

	for _, tc := range []struct {
		name           string
		flagValuesPath string
		want           []string
	}{
		{"with --values", "custom.yaml", []string{"custom.yaml"}},
		{"without --values", "", configValuesFiles},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := helmValuesFiles(tc.flagValuesPath, configValuesFiles); !slices.Equal(got, tc.want) {
				t.Errorf("helmValuesFiles(%q, %v) = %v, want %v", tc.flagValuesPath, configValuesFiles, got, tc.want)
			}
		})
	}
}

// Each deploy command refuses, in Prepare, a --helm-chart-repo or --values that
// the project config would refuse in their place, before Run resolves the
// environment, which may ask to log in first. The refusal is a usage error
// that names the flag.
func TestDeploy_PrepareRefusesWhatTheProjectConfigWould(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name string, contents string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	valuesFile := writeFile("values.yaml", "replicas: 1\n")
	valuesDir := filepath.Join(dir, "values.d.yaml")
	if err := os.Mkdir(valuesDir, 0o700); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name            string
		chartRepository string
		valuesPath      string
		refusedFlag     string // empty where Prepare accepts
	}{
		{name: "no overrides"},
		{name: "chart repository", chartRepository: "https://charts.example.com"},
		{name: "values file", valuesPath: valuesFile},
		{name: "chart repository with no scheme", chartRepository: "charts.example.com", refusedFlag: "--helm-chart-repo"},
		{name: "chart repository with another scheme", chartRepository: "oci://charts.example.com", refusedFlag: "--helm-chart-repo"},
		{name: "values file that is not there", valuesPath: filepath.Join(dir, "missing.yaml"), refusedFlag: "--values"},
		{name: "values directory", valuesPath: valuesDir, refusedFlag: "--values"},
		{name: "values file not named as YAML", valuesPath: writeFile("values.txt", "replicas: 1\n"), refusedFlag: "--values"},
		{name: "values file that is not YAML", valuesPath: writeFile("broken.yaml", "replicas: [1\n"), refusedFlag: "--values"},
	}

	commands := []struct {
		name    string
		prepare func(chartRepository string, valuesPath string) error
	}{
		{
			name: "deploy server",
			prepare: func(chartRepository string, valuesPath string) error {
				o := deployGameServerOpts{flagHelmChartRepository: chartRepository, flagHelmValuesPath: valuesPath}
				return o.Prepare(nil, nil)
			},
		},
		{
			name: "deploy botclient",
			prepare: func(chartRepository string, valuesPath string) error {
				o := deployBotClientOpts{argImageTag: "20260601-153000-1a27c25", flagHelmChartRepository: chartRepository, flagHelmValuesPath: valuesPath}
				return o.Prepare(nil, nil)
			},
		},
	}
	for _, command := range commands {
		for _, tc := range cases {
			t.Run(command.name+"/"+tc.name, func(t *testing.T) {
				err := command.prepare(tc.chartRepository, tc.valuesPath)
				if tc.refusedFlag == "" {
					if err != nil {
						t.Errorf("refused: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatal("accepted")
				}
				if !clierrors.IsUsageError(err) {
					t.Errorf("error = %v, want a usage error", err)
				}
				if !strings.Contains(err.Error(), tc.refusedFlag) {
					t.Errorf("error %q does not name %s", err, tc.refusedFlag)
				}
			})
		}
	}
}
