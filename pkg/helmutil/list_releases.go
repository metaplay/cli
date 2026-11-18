/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/release"
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

	// Helm v4 returns the Releaser interface, so convert to concrete releases.
	releases := make([]*v1.Release, 0, len(releasers))
	for _, releaser := range releasers {
		rel, err := toV1Release(releaser)
		if err != nil {
			return nil, err
		}
		releases = append(releases, rel)
	}

	log.Debug().Msgf("Found %d Helm releases: %s", len(releases), strings.Join(GetReleaseNames(releases), ", "))

	// Filter releases by chart name
	var filteredReleases []*v1.Release
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

func GetReleaseNames(releases []*v1.Release) []string {
	names := make([]string, len(releases))
	for ndx, rel := range releases {
		names[ndx] = rel.Name
	}
	return names
}

// toV1Release converts a release returned by a Helm action to the concrete type.
// Helm v4 returns the Releaser interface to leave room for future release
// formats. Releases of apiVersion v1 and v2 charts are all v1.Release.
func toV1Release(releaser release.Releaser) (*v1.Release, error) {
	switch rel := releaser.(type) {
	case *v1.Release:
		return rel, nil
	case v1.Release:
		return &rel, nil
	default:
		return nil, fmt.Errorf("unexpected Helm release type: %T", releaser)
	}
}
