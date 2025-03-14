/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package helmutil

import (
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/cli/values"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/release"
)

// HelmUpgradeOrInstall performs the equivalent of `helm upgrade --install --wait --values <path> ...`
func HelmUpgradeOrInstall(
	actionConfig *action.Configuration,
	existingRelease *release.Release,
	namespace, releaseName, chartURL string,
	baseValues map[string]interface{},
	valuesFiles []string,
	timeout time.Duration) (*release.Release, error) {
	// Construct the command to use:
	// - Use install if no previous Helm release exists
	// - Use upgrade if a previous Helm release exists
	var installCmd *action.Install
	var upgradeCmd *action.Upgrade
	var chartPathOptions *action.ChartPathOptions
	if existingRelease == nil {
		// Create Helm release install action
		installCmd = action.NewInstall(actionConfig)
		installCmd.ReleaseName = releaseName
		installCmd.Namespace = namespace
		installCmd.Wait = true
		installCmd.Timeout = timeout
		chartPathOptions = &installCmd.ChartPathOptions

		log.Debug().Msgf("Install new Helm release...")
	} else {
		// Create Helm release upgrade action
		upgradeCmd = action.NewUpgrade(actionConfig)
		upgradeCmd.Namespace = namespace
		upgradeCmd.Install = true // \note NOT the same as 'helm upgrade --install' !!
		upgradeCmd.Wait = true
		upgradeCmd.Timeout = timeout
		chartPathOptions = &upgradeCmd.ChartPathOptions

		log.Debug().Msgf("Upgrade existing Helm release...")
		if existingRelease.Name != releaseName {
			log.Warn().Msgf("Mismatched Helm release name: existing release is named '%s', updating with name '%s'", existingRelease.Name, releaseName)
			releaseName = existingRelease.Name
		}
	}

	// Locate (download) chart.
	helmClient := cli.New()
	chartPath, err := chartPathOptions.LocateChart(chartURL, helmClient)
	if err != nil {
		return nil, fmt.Errorf("failed to locate Helm chart: %w", err)
	}

	// Load the chart from the resolved path.
	loadedChart, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load Helm chart: %w", err)
	}

	// Load values from files.
	filesValueOpts := &values.Options{
		ValueFiles: valuesFiles,
	}
	filesValueMap, err := filesValueOpts.MergeValues(getter.All(helmClient))
	if err != nil {
		return nil, fmt.Errorf("failed to load Helm values files: %w", err)
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
	if installCmd != nil {
		release, err := installCmd.Run(loadedChart, finalValueMap)
		if err != nil {
			return nil, fmt.Errorf("failed to install the Helm chart: %w", err)
		}

		log.Debug().Msg("Helm install success")
		return release, nil
	} else {
		release, err := upgradeCmd.Run(releaseName, loadedChart, finalValueMap)
		if err != nil {
			return nil, fmt.Errorf("failed to upgrade an existing Helm release: %w", err)
		}

		log.Debug().Msg("Helm upgrade success")
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
