/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package portalapi

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strconv"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
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
	_, err := metahttp.PutJSON[any](c.httpClient, fmt.Sprintf("/api/v1/contract_signatures"), payload)
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
			return "", clierrors.New("SDK download requires accepting the terms and conditions").
				WithSuggestion("Visit https://portal.metaplay.dev to accept the SDK terms and conditions")
		}
		return "", clierrors.Newf("Failed to download the Metaplay SDK (status %d)", resp.StatusCode()).
			WithSuggestion("Check your network connection and try again")
	}

	log.Debug().Msgf("Downloaded SDK to %s", tmpSdkZipPath)
	return tmpSdkZipPath, nil
}

// Fetch the organizations and projects (within each org) that the user has access to.
// Note: It's considered an error if the user has no accessible organizations.
func (c *Client) FetchUserOrgsAndProjects() ([]OrganizationWithProjects, error) {
	path := fmt.Sprintf("/api/v1/organizations/user-organizations")
	orgsWithProjects, err := metahttp.Get[[]OrganizationWithProjects](c.httpClient, path)
	if err != nil {
		return nil, fmt.Errorf("failed to list user's organizations and projects: %w", err)
	}

	// It's an error if the user has no accessible organizations.
	if len(orgsWithProjects) == 0 {
		return nil, clierrors.New("No accessible organizations found").
			WithSuggestion("Create a new organization at https://portal.metaplay.dev, or request access to an existing one from your team")
	}

	// Sanity check the returned data.
	for _, org := range orgsWithProjects {
		for _, project := range org.Projects {
			if project.HumanID == "" {
				return nil, fmt.Errorf("internal error: portal project '%s' has empty human ID", project.Name)
			}
		}
	}

	return orgsWithProjects, err
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
		return nil, clierrors.Newf("Project '%s' not found", projectHumanID).
			WithSuggestion("Check the project ID is correct, or run 'metaplay auth whoami' to verify your account has access")
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
		return nil, clierrors.Newf("Environment '%s' not found", humanID).
			WithSuggestion("Run 'metaplay update project-environments' to sync with portal, or check available environments in the portal")
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
// If only a major version is provided (e.g., "34"), returns the latest minor/patch for that major.
// Returns nil if no matching version is found.
func (c *Client) FindSdkVersionByVersionOrName(versionOrName string) (*SdkVersionInfo, error) {
	log.Debug().Msgf("FindSdkVersionByVersionOrName: looking for '%s'", versionOrName)

	// Get all SDK versions
	versions, err := c.GetSdkVersions()
	if err != nil {
		return nil, fmt.Errorf("failed to get SDK versions: %w", err)
	}

	// If input looks like a major version only (digits with no dots), find the latest for that major.
	// Check this before name matching, so "34" finds latest 34.x instead of matching a name.
	if isMajorVersionOnly(versionOrName) {
		result, err := findLatestForMajorVersion(versions, versionOrName)
		if err != nil {
			return nil, err
		}
		if result != nil {
			log.Debug().Msgf("Resolved major version %s to %s", versionOrName, result.Version)
			return result, nil
		}
		// No "X.y" versions found, fall through to check for exact match (e.g., "36" without minor versions)
		log.Debug().Msgf("No minor versions found for major version %s, checking for exact match", versionOrName)
	}

	// Try to find an exact match for the version string.
	for _, v := range versions {
		if v.Version == versionOrName {
			if v.StoragePath == nil {
				return nil, fmt.Errorf("SDK version '%s' found but it has no downloadable file", versionOrName)
			}
			return &v, nil
		}
	}

	// If no exact version match, try to find a match in the name
	for _, v := range versions {
		if v.Name == versionOrName {
			if v.StoragePath == nil {
				return nil, fmt.Errorf("SDK version with name '%s' found but it has no downloadable file", versionOrName)
			}
			return &v, nil
		}
	}

	// No match found
	return nil, nil
}

// isMajorVersionOnly checks if the input is a major version number only (e.g., "34" but not "34.3")
func isMajorVersionOnly(version string) bool {
	if version == "" {
		return false
	}
	for _, c := range version {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// findLatestForMajorVersion finds the latest SDK version matching the given major version.
func findLatestForMajorVersion(versions []SdkVersionInfo, majorVersion string) (*SdkVersionInfo, error) {
	prefix := majorVersion + "."
	var best *SdkVersionInfo
	var bestMinor, bestPatch int = -1, -1

	for i := range versions {
		v := &versions[i]
		if !strings.HasPrefix(v.Version, prefix) {
			continue
		}
		if v.StoragePath == nil {
			continue
		}

		rest := strings.TrimPrefix(v.Version, prefix)
		minor, patch := parseMinorPatch(rest)

		if minor > bestMinor || (minor == bestMinor && patch > bestPatch) {
			best = v
			bestMinor = minor
			bestPatch = patch
		}
	}

	return best, nil
}

// parseMinorPatch parses "3" or "3.1" into minor and patch numbers.
func parseMinorPatch(s string) (minor, patch int) {
	parts := strings.SplitN(s, ".", 2)
	if len(parts) >= 1 {
		minor, _ = strconv.Atoi(parts[0])
	}
	if len(parts) >= 2 {
		patch, _ = strconv.Atoi(parts[1])
	}
	return minor, patch
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
