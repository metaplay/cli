package helmutil

import (
	"fmt"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/cli/values"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/release"
)

// HelmUpgradeInstall performs the equivalent of `helm upgrade --install --wait --values <path> --set-string image.tag=<tag>`
func HelmUpgradeInstall(actionConfig *action.Configuration, existingRelease *release.Release, namespace, releaseName, chartURL, valuesFile, imageTag string, timeout time.Duration) (*release.Release, error) {
	// Construct the command to use:
	// - Use install if no previous Helm release exists
	// - Use upgrade if a previous Helm release exists
	var installCmd *action.Install
	var upgradeCmd *action.Upgrade
	var chartPathOptions *action.ChartPathOptions
	if existingRelease == nil {
		installCmd = action.NewInstall(actionConfig)
		installCmd.ReleaseName = releaseName
		installCmd.Namespace = namespace
		installCmd.Wait = true
		installCmd.Timeout = timeout

		chartPathOptions = &installCmd.ChartPathOptions
	} else {
		// Create Helm upgrade install upgrade
		upgradeCmd = action.NewUpgrade(actionConfig)
		upgradeCmd.Namespace = namespace
		upgradeCmd.Install = true // \note NOT the same as 'helm upgrade --install' !!
		upgradeCmd.Wait = true
		upgradeCmd.Timeout = timeout

		chartPathOptions = &upgradeCmd.ChartPathOptions
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

	// Load values from file and set additional values
	valOpts := &values.Options{
		ValueFiles: []string{valuesFile},
		StringValues: []string{
			fmt.Sprintf("image.tag=%s", imageTag),
		},
	}

	// \todo is this needed only when overriding values?
	valMap, err := valOpts.MergeValues(getter.All(helmClient))
	if err != nil {
		return nil, fmt.Errorf("failed to merge Helm chart values: %w", err)
	}

	// Run install or upgrade install
	if installCmd != nil {
		release, err := installCmd.Run(loadedChart, valMap)
		if err != nil {
			return nil, fmt.Errorf("failed to install the Helm chart: %w", err)
		}

		return release, nil
	} else {
		release, err := upgradeCmd.Run(releaseName, loadedChart, valMap)
		if err != nil {
			return nil, fmt.Errorf("failed to upgrade an existing Helm release: %w", err)
		}

		return release, nil
	}
}
