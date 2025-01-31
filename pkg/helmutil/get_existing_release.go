/*
 * Copyright Metaplay. All rights reserved.
 */
package helmutil

import (
	"fmt"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
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
		return nil, fmt.Errorf("multiple Helm releases found! Remove them first using the matching 'metaplay environment uninstall-*' command.")
	}

	// Handle single release.
	existingRelease := releases[0]
	return existingRelease, nil
}
