/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"fmt"

	clierrors "github.com/metaplay/cli/internal/errors"
	"helm.sh/helm/v4/pkg/action"
	v1 "helm.sh/helm/v4/pkg/release/v1"
)

const (
	wellKnownGameServerChartName = "metaplay-gameserver"
	wellKnownBotClientChartName  = "metaplay-loadtest"
)

// Find an existing Helm relase with the given chart name.
// If multiple releases are found, it is considered an error.
func GetExistingRelease(actionConfig *action.Configuration, chartName string) (*v1.Release, error) {
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
		switch chartName {
		case wellKnownGameServerChartName:
			return nil, clierrors.New("Multiple Helm releases found").
				WithSuggestion("Remove them first with 'metaplay remove server'")
		case wellKnownBotClientChartName:
			return nil, clierrors.New("Multiple Helm releases found").
				WithSuggestion("Remove them first with 'metaplay remove botclient'")
		default:
			return nil, clierrors.Newf("Multiple Helm releases found for chart %q", chartName).
				WithSuggestion("Remove the existing release first")
		}
	}

	// Handle single release.
	existingRelease := releases[0]
	return existingRelease, nil
}
