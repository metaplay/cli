/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
)

// HelmListReleases lists all Helm releases in the specified namespace
// that match the specified chartName.
func HelmListReleases(actionConfig *action.Configuration, chartName string) ([]*release.Release, error) {
	// Create Helm List action
	list := action.NewList(actionConfig)
	list.AllNamespaces = false // restrict to the given namespace
	list.All = true            // include all releases, even those in failed or other states
	list.SetStateMask()        // make sure the derived state is up-to-date

	// Execute the List action
	releases, err := list.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to list Helm releases: %w", err)
	}
	log.Debug().Msgf("Found %d Helm releases: %s", len(releases), strings.Join(GetReleaseNames(releases), ", "))

	// Filter releases by chart name
	var filteredReleases []*release.Release
	for _, rel := range releases {
		if rel.Chart != nil && rel.Chart.Metadata != nil {
			if rel.Chart.Metadata.Name == chartName {
				filteredReleases = append(filteredReleases, rel)
			}
		} else {
			log.Warn().Msgf("Found chart with missing metadata: %s", rel.Name)
		}
	}

	return filteredReleases, nil
}

func GetReleaseNames(releases []*release.Release) []string {
	names := make([]string, len(releases))
	for ndx, release := range releases {
		names[ndx] = release.Name
	}
	return names
}
