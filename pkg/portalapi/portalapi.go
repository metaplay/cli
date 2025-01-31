/*
 * Copyright Metaplay. All rights reserved.
 */
package portalapi

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/common"
	"github.com/metaplay/cli/pkg/metahttp"
	"github.com/rs/zerolog/log"
)

// NewClient creates a new Portal API client with the given auth token set.
func NewClient(tokenSet *auth.TokenSet) *Client {
	return &Client{
		httpClient: metahttp.NewClient(tokenSet, common.PortalBaseURL),
		baseURL:    common.PortalBaseURL,
		tokenSet:   tokenSet,
	}
}

// DownloadLatestSdk downloads the latest SDK to the specified target directory.
func (c *Client) DownloadLatestSdk(targetDir string) (string, error) {
	// Download the SDK to a temp file.
	path := fmt.Sprintf("/download/sdk")
	tmpFilename := fmt.Sprintf("metaplay-sdk-%08x.zip", rand.Uint32())
	tmpSdkZipPath := filepath.Join(targetDir, tmpFilename)
	resp, err := metahttp.Download(c.httpClient, path, tmpSdkZipPath)
	if err != nil {
		return "", fmt.Errorf("failed to download SDK: %w", err)
	}

	// Handle server errors.
	if resp.IsError() {
		if resp.StatusCode() == 403 {
			return "", fmt.Errorf("you must sign the SDK terms and conditions in https://portal.metaplay.dev first")
		}
		return "", fmt.Errorf("failed to download the Metaplay SDK from the portal with status code %d: %s", resp.StatusCode(), string(resp.Body()))
	}

	log.Debug().Msgf("Downloaded SDK to %s", tmpSdkZipPath)
	return tmpSdkZipPath, nil
}

func (c *Client) FetchAllOrganizations() ([]PortalOrganizationInfo, error) {
	url := fmt.Sprintf("/api/v1/organizations")
	organizations, err := metahttp.Get[[]PortalOrganizationInfo](c.httpClient, url)
	if err != nil {
		return nil, fmt.Errorf("failed to list organizations: %w", err)
	}

	return organizations, nil
}

func (c *Client) FetchAllProjects() ([]PortalProjectInfo, error) {
	url := fmt.Sprintf("/api/v1/projects")
	projects, err := metahttp.Get[[]PortalProjectInfo](c.httpClient, url)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}

	// \todo Filling in dummy humanIDs as we don't yet have them
	for ndx, proj := range projects {
		projects[ndx].HumanID = fmt.Sprintf("dummy-%s", proj.Slug)
	}

	return projects, nil
}

// FetchProjectInfo fetches information about a project using its human ID.
func (c *Client) FetchProjectInfo(projectHumanID string) (*PortalProjectInfo, error) {
	url := fmt.Sprintf("/api/v1/projects?human_id=%s", projectHumanID)
	projectInfos, err := metahttp.Get[[]PortalProjectInfo](c.httpClient, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch environment details: %w", err)
	}

	log.Debug().Msgf("Project info response from portal: %+v", projectInfos)
	if len(projectInfos) == 0 {
		return nil, fmt.Errorf("portal did not return any projects")
	} else if len(projectInfos) > 2 {
		return nil, fmt.Errorf("portal returned %d matching projects, expecting only one", len(projectInfos))
	}

	return &projectInfos[0], nil
}

// FetchProjectEnvironments fetches all environments for the given project.
func (c *Client) FetchProjectEnvironments(projectUUID string) ([]PortalEnvironmentInfo, error) {
	url := fmt.Sprintf("/api/v1/environments?projectId=%s", projectUUID)
	environmentInfos, err := metahttp.Get[[]PortalEnvironmentInfo](c.httpClient, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch environment details: %w", err)
	}

	log.Debug().Msgf("Environments info response from portal:\n%+v\n", environmentInfos)
	return environmentInfos, nil
}

// FetchEnvironmentInfoByHumanID fetches information about an environment using its human ID.
func (c *Client) FetchEnvironmentInfoByHumanID(humanID string) (*PortalEnvironmentInfo, error) {
	url := fmt.Sprintf("/api/v1/environments?human_id=%s", humanID)
	envInfos, err := metahttp.Get[[]PortalEnvironmentInfo](c.httpClient, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch environment details from portal: %w", err)
	}

	if len(envInfos) == 0 {
		return nil, fmt.Errorf("failed to fetch environment details from portal: no such environment")
	}

	if len(envInfos) > 1 {
		return nil, fmt.Errorf("failed to fetch environment details from portal: multiple results returned")
	}

	return &envInfos[0], nil
}
