/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package helmutil

import (
	"fmt"
	"time"

	"github.com/metaplay/cli/internal/tui"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
)

// HelmUpgradeOrInstall performs the equivalent of `helm upgrade --install --wait --values <path> ...`
func HelmUpgradeOrInstall(
	output *tui.TaskOutput,
	actionConfig *action.Configuration,
	existingRelease *release.Release,
	namespace, releaseName, chartURL string,
	chartVersion string,
	valuesFiles []string,
	extraValues map[string]interface{},
	timeout time.Duration,
) (*release.Release, error) {
	// Show header at top
	headerLine := fmt.Sprintf("Deploying chart %s as release %s", chartURL, releaseName)
	output.SetHeaderLines([]string{headerLine})

	// Pipe Helm output to task output
	actionConfig.Log = func(format string, args ...interface{}) {
		output.AppendLinef(format, args)
	}

	var installCmd *action.Install
	var upgradeCmd *action.Upgrade
	var chartPathOptions *action.ChartPathOptions

	// Determine if install or upgrade based on existence of release:
	// - Use install if no previous Helm release exists
	// - Use upgrade if a previous Helm release exists
	if existingRelease == nil {
		output.AppendLine("No existing release found, install new release")
		installCmd = action.NewInstall(actionConfig)
		installCmd.Version = chartVersion
		installCmd.ReleaseName = releaseName
		installCmd.Namespace = namespace
		installCmd.Wait = true
		installCmd.Timeout = timeout
		installCmd.Devel = true // If version is development, accept it
		chartPathOptions = &installCmd.ChartPathOptions
	} else {
		output.AppendLinef("Existing release found (version %s), upgrade existing release", existingRelease.Chart.Metadata.Version)
		upgradeCmd = action.NewUpgrade(actionConfig)
		upgradeCmd.Version = chartVersion
		upgradeCmd.Namespace = namespace
		upgradeCmd.Wait = true
		upgradeCmd.Timeout = timeout
		upgradeCmd.MaxHistory = 10      // Keep 10 releases max
		upgradeCmd.Devel = true         // If version is development, accept it
		upgradeCmd.Atomic = false       // Don't rollback on failures to not hide errors
		upgradeCmd.CleanupOnFail = true // Clean resources on failure
		chartPathOptions = &upgradeCmd.ChartPathOptions
	}

	// Load (download) Helm chart
	output.AppendLine("Loading Helm chart...")

	helmClient := cli.New()
	chartPath, err := chartPathOptions.LocateChart(chartURL, helmClient)
	if err != nil {
		return nil, fmt.Errorf("failed to locate Helm chart: %w", err)
	}

	output.AppendLinef("Loading chart from: %s", chartPath)
	loadedChart, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load Helm chart: %w", err)
	}

	output.AppendLinef("Chart loaded: %s (version %s)", loadedChart.Name(), loadedChart.Metadata.Version)

	// Construct base values
	baseValues := map[string]interface{}{}
	if extraValues != nil {
		baseValues = extraValues
	}

	// Load values from files if any
	filesValueMap := map[string]interface{}{}
	for _, valuesFile := range valuesFiles {
		output.AppendLinef("Loading values from: %s", valuesFile)
		values, err := chartutil.ReadValuesFile(valuesFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read values file: %w", err)
		}

		// Merge with previous values, files processed later override earlier ones
		filesValueMap = mergeValuesMaps(filesValueMap, values.AsMap())
	}

	// Resolve final values map: use extraValues as base to allow files to override any defaults.
	finalValueMap := mergeValuesMaps(baseValues, filesValueMap)

	// Log values as YAML.
	finalValuesYAML, err := yaml.Marshal(finalValueMap)
	if err != nil {
		log.Warn().Msgf("Failed to marshal values as YAML: %+v", finalValueMap)
	} else {
		log.Debug().Msgf("Default Helm values:\n%s", finalValuesYAML)
	}

	// Run install or upgrade install
	output.AppendLine("Starting Helm deployment...")
	if installCmd != nil {
		output.AppendLine("Installing new release...")
		release, err := installCmd.Run(loadedChart, finalValueMap)
		if err != nil {
			return nil, fmt.Errorf("failed to install the Helm chart: %w", err)
		}
		return release, nil
	} else {
		output.AppendLine("Upgrading existing release...")
		release, err := upgradeCmd.Run(releaseName, loadedChart, finalValueMap)
		if err != nil {
			return nil, fmt.Errorf("failed to upgrade an existing Helm release: %w", err)
		}
		return release, nil
	}
}

// Combine two Helm values maps into one. On conflicts, the fields in 'override' win
// over 'base'. Maps are recursively merged. Sequences are replaced.
func mergeValuesMaps(base, override map[string]interface{}) map[string]interface{} {
	// Clone base.
	combined := make(map[string]interface{}, len(base))
	for k, v := range base {
		combined[k] = v
	}

	// Merge all keys from override (recursively merge maps).
	for k, v := range override {
		if v, ok := v.(map[string]interface{}); ok {
			if bv, ok := combined[k]; ok {
				if bv, ok := bv.(map[string]interface{}); ok {
					combined[k] = mergeValuesMaps(bv, v)
					continue
				}
			}
		}
		combined[k] = v
	}
	return combined
}
