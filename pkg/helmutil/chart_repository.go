/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/pkg/metahttp"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

func ValidateLocalHelmChart(helmChartLocalPath string) error {
	// Helm chart local path must exist and be a directory.
	info, err := os.Stat(helmChartLocalPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("path to Helm chart is not a directory")
	}

	// Read Chart.yaml.
	chartBytes, err := os.ReadFile(helmChartLocalPath + "/Chart.yaml")
	if err != nil {
		return fmt.Errorf("failed to read Chart.yaml in directory %s", helmChartLocalPath)
	}

	// Parse Chart data.
	type HelmChart struct {
		APIVersion  string `yaml:"apiVersion"`
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Version     string `yaml:"version"`
	}

	// Parse the YAML.
	var chart HelmChart
	err = yaml.Unmarshal(chartBytes, &chart)
	if err != nil {
		return fmt.Errorf("failed to parse Chart.yaml: %v", err)
	}

	// Chart name must be 'metaplay-gameserver'.
	if chart.Name != "metaplay-gameserver" {
		return fmt.Errorf("invalid chart name: %s (expected 'metaplay-gameserver')", chart.Name)
	}

	return nil
}

// Fetch all the charts with the specified name and satisfying the version filter
// from the Helm chart repository.
func FetchHelmChartVersions(repository string, chartName string, minVersion *version.Version) ([]string, error) {
	// HelmChartEntry represents an entry for a specific chart version.
	type HelmChartEntry struct {
		Version string `yaml:"version"`
	}

	// HelmChartRepoIndex represents the index of the Helm chart repository.
	type HelmChartRepoIndex struct {
		APIVersion string                      `yaml:"apiVersion"`
		Entries    map[string][]HelmChartEntry `yaml:"entries"`
		Generated  string                      `yaml:"generated"`
	}

	// Fetch the index.yaml file from the repository
	url := strings.TrimSuffix(repository, "/") + "/index.yaml"
	log.Debug().Msgf("Fetching Helm chart versions from '%s'...", url)

	body, err := metahttp.GetBytesWithRetry(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch repository index: %w", err)
	}

	// Parse the YAML index file
	var repoIndex HelmChartRepoIndex
	if err := yaml.Unmarshal(body, &repoIndex); err != nil {
		return nil, fmt.Errorf("failed to parse chart repository index.yaml: %w", err)
	}

	// Grab all versions >= 0.5.0 -- older are considered legacy
	var filteredVersions []string
	if chartEntries, found := repoIndex.Entries[chartName]; found {
		for _, entry := range chartEntries {
			v, err := version.NewVersion(entry.Version)
			if err != nil {
				log.Warn().Msgf("Skipping invalid Helm chart version '%s': %v", entry.Version, err)
				continue
			}

			// Only keep versions that are at least the minVersion.
			if v.Compare(minVersion) >= 0 {
				filteredVersions = append(filteredVersions, entry.Version)
			}
		}
	} else {
		return nil, fmt.Errorf("no entries found for chart '%s'", chartName)
	}

	return filteredVersions, nil
}

// ResolveBestMatchingVersion resolves the best matching version from a list of versions that satisfy the constraint.
// If the constraint is nil, all versions are considered valid.
func ResolveBestMatchingVersion(availableVersions []string, constraints version.Constraints) (string, error) {
	// Filter versions that satisfy the range
	var satisfyingVersions []*version.Version
	for _, vStr := range availableVersions {
		v, err := version.NewVersion(vStr)
		if err != nil {
			log.Warn().Msgf("Skipping invalid Helm chart version '%s': %v", vStr, err)
			continue
		}

		if constraints == nil || constraints.Check(v) {
			satisfyingVersions = append(satisfyingVersions, v)
		}
	}

	log.Debug().Msgf("Version satisfying constraint: %v", satisfyingVersions)

	// If no versions satisfy the range, return an error
	if len(satisfyingVersions) == 0 {
		return "", fmt.Errorf("no matching Helm chart versions found")
	}

	// Sort the versions in descending order
	sort.Sort(sort.Reverse(version.Collection(satisfyingVersions)))

	// Return the highest version
	return satisfyingVersions[0].String(), nil
}

// Find the best matching Helm chart version from a remote chart repository.
// The returned version is the latest of the charts satisfying the rules:
// a) has the specified chart name, b) is newer than the legacy version cut-off,
// c) matches the version constraint.
func ResolveBestMatchingHelmVersion(helmChartRepo, chartName string, legacyVersionCutoff *version.Version, versionConstraints version.Constraints) (string, error) {
	// Fetch recent Helm chart versions (ignore all legacy version already here).
	helmChartRepo = strings.TrimSuffix(helmChartRepo, "/")
	availableChartVersions, err := FetchHelmChartVersions(helmChartRepo, chartName, legacyVersionCutoff)
	if err != nil {
		return "", fmt.Errorf("failed to fetch Helm chart versions from the repository: %v", err)
	}
	log.Debug().Msgf("Available Helm chart versions in repository: %v", strings.Join(availableChartVersions, ", "))

	// Find the best version match that is the latest one from the versions satisfying the requested version(s).
	useChartVersion, err := ResolveBestMatchingVersion(availableChartVersions, versionConstraints)
	if err != nil {
		return "", fmt.Errorf("failed to find a matching Helm chart version: %v", err)
	}

	return useChartVersion, nil
}

// Construct final Helm chart path for a remote chart.
func GetHelmChartPath(helmChartRepo, chartName, chartVersion string) string {
	helmChartRepo = strings.TrimSuffix(helmChartRepo, "/")
	return fmt.Sprintf("%s/%s-%s.tgz", helmChartRepo, chartName, chartVersion)
}
