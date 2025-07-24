/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"fmt"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
)

const (
	wellKnownGameServerChartName = "metaplay-gameserver"
	wellKnownBotClientChartName  = "metaplay-loadtest"
)

// Find an existing Helm relase with the given chart name.
// If multiple releases are found, it is considered an error.
func GetExistingRelease(actionConfig *action.Configuration, chartName string) (*release.Release, error) {
	// Find all releases of the chart deployed in the environment.
	releases, err := HelmListReleases(actionConfig, chartName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve existing Helm releases: %v", err)
	}

	// Handle no found releases.
	if len(releases) == 0 {
		return nil, nil
	}

	// Handle multiple found releases.
	if len(releases) > 1 {
		if chartName == wellKnownGameServerChartName {
			return nil, fmt.Errorf("multiple Helm releases found! Remove them first using 'metaplay remove server' command.")
		} else if chartName == wellKnownBotClientChartName {
			return nil, fmt.Errorf("multiple Helm releases found! Remove them first using 'metaplay remove botclient' command.")
		} else {
			return nil, fmt.Errorf("multiple Helm releases found! Remove release for chart %q first.", chartName)
		}
	}

	// Handle single release.
	existingRelease := releases[0]
	return existingRelease, nil
}
