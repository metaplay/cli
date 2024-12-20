package helmutil

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
)

// HelmListReleases lists all Helm releases in the specified namespace.
func HelmListReleases(actionConfig *action.Configuration, chartName string) ([]*release.Release, error) {
	// Create Helm List action
	list := action.NewList(actionConfig)
	list.AllNamespaces = false // restrict to the given namespace
	list.All = true            // include all releases, even those in failed or other states

	// Execute the List action
	releases, err := list.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to list Helm releases: %w", err)
	}

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
