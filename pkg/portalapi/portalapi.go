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

// Status of a single contract for a user.
type UserContractStatus struct {
	Version    string `json:"version"`     // Version of contract.
	AcceptedAt string `json:"accepted_at"` // Timestamp when contract accepted.
}

// User profile in the portal.
type UserProfile struct {
	UserID         string                        `json:"id"`              // Portal user ID (different from Metaplay Auth).
	ContractStatus map[string]UserContractStatus `json:"contract_status"` // Contract statuses for user.
}

// Check whether the user has agreed to the general terms and conditions, i.e.,
// are they allowed to download the SDK.
func (c *Client) IsGeneralTermsAndConditionsAccepted() (bool, error) {
	// Fetch my user profile from portal.
	myProfile, err := metahttp.Get[UserProfile](c.httpClient, "/api/v1/users/me")
	if err != nil {
		return false, err
	}
	log.Debug().Msgf("User profile: %+v", myProfile)

	// Get General Terms & Conditions (prerequisite for downloading the SDK) status.
	_, found := myProfile.ContractStatus["general-terms-and-conditions"]
	if !found {
		return false, nil
	}

	return true, nil
}

func (c *Client) AgreeToGeneralTermsAndConditions() error {
	// Fill in the request.
	payload := map[string]interface{}{
		"contract_status": map[string]interface{}{
			"general-terms-and-conditions": map[string]interface{}{
				"version":     "", // Version is handled on the server
				"accepted_at": "", // The timestamp is handled on the server
			},
		},
	}

	// POST to the user to update the contract status.
	_, err := metahttp.Post[any](c.httpClient, fmt.Sprintf("/api/v1/users/me"), payload)
	if err != nil {
		return fmt.Errorf("failed to agree to general terms and conditions: %w", err)
	}

	return nil
}

// DownloadSdk downloads the latest SDK to the specified target directory.
// Use sdkVersion == "" for latest.
func (c *Client) DownloadSdk(targetDir, sdkVersion string) (string, error) {
	// Download the SDK to a temp file.
	// \todo hoist version handling to happen earlier & always use versioned download link here?
	var path string
	if sdkVersion != "" {
		path = fmt.Sprintf("/download/sdk?sdk_version=%s", sdkVersion)
	} else {
		path = "/download/sdk" // defaults to latest
	}
	tmpFilename := fmt.Sprintf("metaplay-sdk-%08x.zip", rand.Uint32())
	tmpSdkZipPath := filepath.Join(targetDir, tmpFilename)
	resp, err := metahttp.Download(c.httpClient, path, tmpSdkZipPath)
	if err != nil {
		return "", fmt.Errorf("failed to download SDK: %w", err)
	}

	// Handle server errors.
	if resp.IsError() {
		if resp.StatusCode() == 403 {
			return "", fmt.Errorf("you must agree to the SDK terms and conditions in https://portal.metaplay.dev first")
		}
		return "", fmt.Errorf("failed to download the Metaplay SDK from the portal with status code %d", resp.StatusCode())
	}

	log.Debug().Msgf("Downloaded SDK to %s", tmpSdkZipPath)
	return tmpSdkZipPath, nil
}

// Fetch the organizations and projects (within each org) that the user has access to.
func (c *Client) FetchUserOrgsAndProjects() ([]OrganizationWithProjects, error) {
	path := fmt.Sprintf("/api/v1/organizations/user-organizations")
	orgWithProjects, err := metahttp.Get[[]OrganizationWithProjects](c.httpClient, path)
	if err != nil {
		return nil, fmt.Errorf("failed to list user's organizations and projects: %w", err)
	}
	return orgWithProjects, err
}

func (c *Client) FetchAllUserProjects() ([]ProjectInfo, error) {
	// Fetch with the combined getter.
	orgsWithProjects, err := c.FetchUserOrgsAndProjects()
	if err != nil {
		return nil, err
	}

	// Linearize all projects.
	projects := []ProjectInfo{}
	for _, org := range orgsWithProjects {
		for _, proj := range org.Projects {
			projects = append(projects, proj)
		}
	}

	return projects, nil
}

// FetchProjectInfo fetches information about a project using its human ID.
func (c *Client) FetchProjectInfo(projectHumanID string) (*ProjectInfo, error) {
	url := fmt.Sprintf("/api/v1/projects?human_id=%s", projectHumanID)
	projectInfos, err := metahttp.Get[[]ProjectInfo](c.httpClient, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch environment details: %w", err)
	}

	log.Debug().Msgf("Project info response from portal: %+v", projectInfos)
	if len(projectInfos) == 0 {
		return nil, fmt.Errorf("no project with ID %s found in the Metaplay portal. Are you sure it's correct and you have access?", projectHumanID)
	} else if len(projectInfos) > 2 {
		return nil, fmt.Errorf("portal returned %d matching projects, expecting only one", len(projectInfos))
	}

	return &projectInfos[0], nil
}

// FetchProjectEnvironments fetches all environments for the given project.
func (c *Client) FetchProjectEnvironments(projectUUID string) ([]EnvironmentInfo, error) {
	url := fmt.Sprintf("/api/v1/environments?projectId=%s", projectUUID)
	environmentInfos, err := metahttp.Get[[]EnvironmentInfo](c.httpClient, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch environment details: %w", err)
	}

	log.Debug().Msgf("Environments info response from portal: %+v", environmentInfos)
	return environmentInfos, nil
}

// FetchEnvironmentInfoByHumanID fetches information about an environment using its human ID.
func (c *Client) FetchEnvironmentInfoByHumanID(humanID string) (*EnvironmentInfo, error) {
	url := fmt.Sprintf("/api/v1/environments?human_id=%s", humanID)
	envInfos, err := metahttp.Get[[]EnvironmentInfo](c.httpClient, url)
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
