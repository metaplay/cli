/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"helm.sh/helm/v4/pkg/action"
	v1 "helm.sh/helm/v4/pkg/release/v1"
)

// HelmListReleases lists all Helm releases in the specified namespace
// that match the specified chartName.
func HelmListReleases(actionConfig *action.Configuration, chartName string) ([]*v1.Release, error) {
	// Create Helm List action
	list := action.NewList(actionConfig)
	list.AllNamespaces = false // restrict to the given namespace
	list.All = true            // include all releases, even those in failed or other states
	list.SetStateMask()        // make sure the derived state is up-to-date

	// Execute the List action
	releasers, err := list.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to list Helm releases: %w", err)
	}

	// Convert Releaser interfaces to concrete v1.Release types
	var releases []*v1.Release
	for _, releaser := range releasers {
		if rel, ok := releaser.(*v1.Release); ok {
			releases = append(releases, rel)
		} else {
			log.Warn().Msgf("Unexpected release type: %T", releaser)
		}
	}

	log.Debug().Msgf("Found %d Helm releases: %s", len(releases), strings.Join(GetReleaseNames(releases), ", "))

	// Filter releases by chart name
	var matchingReleases []*v1.Release
	for _, rel := range releases {
		if rel.Chart != nil && rel.Chart.Metadata != nil {
			if rel.Chart.Metadata.Name == chartName {
				matchingReleases = append(matchingReleases, rel)
			}
		} else {
			log.Warn().Msgf("Found chart with missing metadata: %s", rel.Name)
		}
	}

	return matchingReleases, nil
}

func GetReleaseNames(releases []*v1.Release) []string {
	names := make([]string, len(releases))
	for ndx, release := range releases {
		names[ndx] = release.Name
	}
	return names
}
