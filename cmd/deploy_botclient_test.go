/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"reflect"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/envapi"
)

// Bot clients run the game server image, and where the environment's registry
// wants a credential the bots' pods have to name the Secret holding it: the
// operator gives one to the game server's pods only.

func TestBotClientImageValues_NamesThePullSecretTheEnvironmentNames(t *testing.T) {
	values := botClientImageValues(&envapi.EnvironmentImageRepository{
		QualifiedRepository: "registry.example.com/env/gameserver",
		PullSecret:          "env-registry-pull",
	}, "20260601-153000-1a27c25")

	want := map[string]any{
		"repository":  "registry.example.com/env/gameserver",
		"tag":         "20260601-153000-1a27c25",
		"pullSecrets": []any{"env-registry-pull"},
	}
	if !reflect.DeepEqual(values, want) {
		t.Errorf("values = %#v, want %#v", values, want)
	}
}

// Where nodes pull as themselves, as from ECR, the environment names no
// Secret, and the chart is told nothing: it renders exactly what it rendered
// before the value existed.
func TestBotClientImageValues_NamesNoPullSecretWhereTheEnvironmentNamesNone(t *testing.T) {
	values := botClientImageValues(&envapi.EnvironmentImageRepository{
		QualifiedRepository: "123456789012.dkr.ecr.eu-west-1.amazonaws.com/env-gameserver",
	}, "20260601-153000-1a27c25")

	if _, ok := values["pullSecrets"]; ok {
		t.Errorf("values = %#v, want no pullSecrets at all", values)
	}
}

// errorText is everything a user is shown for err: its message, suggestion and
// details.
func errorText(err error) string {
	cliErr, ok := clierrors.AsCLIError(err)
	if !ok {
		return err.Error()
	}
	return strings.Join(append([]string{cliErr.Message, cliErr.Suggestion}, cliErr.Details...), "\n")
}

func loadtestChart(version string, imageValues map[string]any) *chart.Chart {
	return &chart.Chart{
		Metadata: &chart.Metadata{Name: metaplayLoadTestChartName, Version: version},
		Values:   map[string]any{"botclients": map[string]any{"image": imageValues}},
	}
}

// A chart too old to name the Secret installs cleanly, and its pods are then
// refused by the registry until the deploy times out. So it is refused before
// anything is installed, saying how to pick a newer one. It is told apart by
// what it declares rather than by its version, so no version number has to be
// kept in step with the chart's releases.
func TestCheckBotClientChart(t *testing.T) {
	takesPullSecrets := loadtestChart("0.5.0", map[string]any{"repository": nil, "tag": nil, "pullSecrets": []any{}})
	predatesPullSecrets := loadtestChart("0.4.2", map[string]any{"repository": nil, "tag": nil})

	tests := []struct {
		name       string
		chart      *chart.Chart
		pullSecret string
		refused    bool
	}{
		{"a chart that takes pull Secrets, for an environment that names one", takesPullSecrets, "env-registry-pull", false},
		{"a chart that takes pull Secrets, for an environment that names none", takesPullSecrets, "", false},
		{"an older chart, for an environment that names none", predatesPullSecrets, "", false},
		{"an older chart, for an environment that names one", predatesPullSecrets, "env-registry-pull", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkBotClientChart(test.pullSecret)(test.chart)
			if !test.refused {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected the chart to be refused")
			}
			for _, named := range []string{"0.4.2", "botClientChartVersion", "--helm-chart-version"} {
				if !strings.Contains(errorText(err), named) {
					t.Errorf("error %q does not mention %s", errorText(err), named)
				}
			}
		})
	}
}
