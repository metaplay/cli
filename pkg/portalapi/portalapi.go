/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
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
		httpClient: metahttp.NewJSONClient(tokenSet, common.PortalBaseURL),
		baseURL:    common.PortalBaseURL,
		tokenSet:   tokenSet,
	}
}

// User profile in the portal.
type UserProfile struct {
	UserID string `json:"id"` // Portal user ID (different from Metaplay Auth).
}

// Status of a single user contract (signed or not).
type UserCoreContract struct {
	Changes        *string `json:"changes"`
	CreatedAt      string  `json:"created_at"`
	Description    string  `json:"description"`
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	OrganizationID *string `json:"organization_id"`
	Type           string  `json:"type"`
	URI            string  `json:"uri"`
	URIType        string  `json:"uri_type"`
	UserID         *string `json:"user_id"`
	Version        string  `json:"version"`

	// Signature by the user. Nil if not signed.
	ContractSignature *struct {
		ID        string `json:"id"`
		CreatedAt string `json:"created_at"`
	} `json:"contract_signature"`
}

// User state as returned from the /users/me endpoint on portal.
// Includes basic profile info as well as contract signatures.
type UserState struct {
	User UserProfile `json:"user"`

	Contracts struct {
		PrivacyPolicy      UserCoreContract `json:"privacyPolicy"`
		TermsAndConditions UserCoreContract `json:"termsAndConditions"`
	} `json:"contracts"`
}

// Get the user's state from portal /api/v1/users/me endpoint. This includes
// the user profile and contract signature status.
func (c *Client) GetUserState() (*UserState, error) {
	// Fetch my user profile from portal.
	userState, err := metahttp.Get[UserState](c.httpClient, "/api/v1/users/me")
	if err != nil {
		return nil, err
	}
	// log.Info().Msgf("User state: %+v", userState)

	return &userState, nil
}

// User has agreed to the contents of a specific contract. Update the status to the portal.
func (c *Client) AgreeToContract(contractID string) error {
	// Fill in the request.
	payload := map[string]any{
		"contract_id": contractID,
	}

	// POST to the user to update the contract status.
	_, err := metahttp.Put[any](c.httpClient, fmt.Sprintf("/api/v1/contract_signatures"), payload)
	if err != nil {
		return fmt.Errorf("failed to agree to general terms and conditions: %w", err)
	}

	return nil
}

// DownloadSdkByVersionID downloads the SDK with the specified version ID to the target directory.
func (c *Client) DownloadSdkByVersionID(targetDir, versionID string) (string, error) {
	if versionID == "" {
		return "", fmt.Errorf("version ID is required")
	}

	// Download the SDK to a temp file.
	path := fmt.Sprintf("/api/v1/sdk/%s/download", versionID)
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
	log.Debug().Msgf("Fetch project environments by UUID from %s%s", c.httpClient.BaseURL, url)
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
	log.Debug().Msgf("Fetch environment by human ID from %s%s", c.httpClient.BaseURL, url)
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

// GetLatestSdkVersionInfo retrieves information about the latest SDK version.
func (c *Client) GetLatestSdkVersionInfo() (*SdkVersionInfo, error) {
	sdkInfo, err := metahttp.Get[SdkVersionInfo](c.httpClient, "/api/v1/sdk/latest")
	if err != nil {
		return nil, fmt.Errorf("failed to get latest SDK version info: %w", err)
	}
	return &sdkInfo, nil
}

// GetSdkVersions retrieves a list of all available SDK versions.
func (c *Client) GetSdkVersions() ([]SdkVersionInfo, error) {
	sdkVersions, err := metahttp.Get[[]SdkVersionInfo](c.httpClient, "/api/v1/sdk")
	if err != nil {
		return nil, fmt.Errorf("failed to get SDK versions: %w", err)
	}
	return sdkVersions, nil
}

// FindSdkVersionByVersionOrName attempts to find an SDK version by its version string or name.
// Returns nil if no matching version is found.
func (c *Client) FindSdkVersionByVersionOrName(versionOrName string) (*SdkVersionInfo, error) {
	// Get all SDK versions
	versions, err := c.GetSdkVersions()
	if err != nil {
		return nil, fmt.Errorf("failed to get SDK versions: %w", err)
	}

	// First, try to find an exact match for the version string
	for _, v := range versions {
		if v.Version == versionOrName {
			// Check if the version has a storage path
			if v.StoragePath == nil {
				return nil, fmt.Errorf("SDK version '%s' found but it has no downloadable file", versionOrName)
			}
			return &v, nil
		}
	}

	// If no exact version match, try to find a match in the name
	for _, v := range versions {
		if v.Name == versionOrName {
			// Check if the version has a storage path
			if v.StoragePath == nil {
				return nil, fmt.Errorf("SDK version with name '%s' found but it has no downloadable file", versionOrName)
			}
			return &v, nil
		}
	}

	// No match found
	return nil, nil
}

// DownloadLatestSdk downloads the latest SDK to the specified target directory.
// This is a convenience function that combines GetLatestSdkVersionInfo and DownloadSdkByVersionId.
func (c *Client) DownloadLatestSdk(targetDir string) (string, error) {
	// Get the latest SDK version info
	latestSdk, err := c.GetLatestSdkVersionInfo()
	if err != nil {
		return "", fmt.Errorf("failed to get latest SDK version info: %w", err)
	}

	// Check if we have a storage path
	if latestSdk.StoragePath == nil {
		return "", fmt.Errorf("latest SDK version does not have a downloadable file")
	}

	// Download the SDK
	return c.DownloadSdkByVersionID(targetDir, latestSdk.ID)
}
