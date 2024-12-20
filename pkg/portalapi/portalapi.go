package portalapi

import (
	"fmt"

	"github.com/go-resty/resty/v2"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/common"
)

// PortalEnvironmentInfo represents information about an environment received from the portal.
type PortalEnvironmentInfo struct {
	UID         string  `json:"id"`           // UUID of the environment
	ProjectUID  string  `json:"project_id"`   // UUID of the project that this environment belongs to
	Name        string  `json:"name"`         // User-provided name for the environment (can change)
	URL         string  `json:"url"`          // TODO: What is this URL?
	Slug        string  `json:"slug"`         // Slug for the environment (simplified version of name)
	CreatedAt   string  `json:"created_at"`   // Creation time of the environment (ISO8601 string)
	Type        string  `json:"type"`         // Type of the environment (e.g., 'development' or 'production')
	HumanID     string  `json:"human_id"`     // Immutable human-readable identifier, eg, 'tiny-squids'
	EnvDomain   *string `json:"env_domain"`   // Domain that the environment uses (optional)
	StackDomain *string `json:"stack_domain"` // Domain of the infra stack (optional)
}

// FetchInfoBySlugs fetches information about an environment from the Metaplay portal using the environments slugs.
func FetchInfoBySlugs(tokenSet *auth.TokenSet, organizationSlug, projectSlug, environmentSlug string) (*PortalEnvironmentInfo, error) {
	// Make the request
	url := fmt.Sprintf(
		"%s/api/v1/environments/with-slugs?organization_slug=%s&project_slug=%s&environment_slug=%s",
		common.PortalBaseURL, organizationSlug, projectSlug, environmentSlug,
	)
	var envInfo PortalEnvironmentInfo
	resp, err := resty.New().R().
		SetAuthToken(tokenSet.AccessToken). // Set Bearer token for Authorization
		SetResult(&envInfo).                // Unmarshal response into the struct
		Get(url)

	if err != nil {
		return nil, fmt.Errorf("failed to fetch environment details: %w", err)
	}

	if resp.IsError() {
		return nil, fmt.Errorf("API error: %s", resp.Status())
	}

	return &envInfo, nil
}

// FetchInfoBySlugs fetches information about an environment from the Metaplay portal using the environment's humanID.
func FetchInfoByHumanID(tokenSet *auth.TokenSet, humanID string) (*PortalEnvironmentInfo, error) {
	// Make the request
	url := fmt.Sprintf("%s/api/v1/environments?human_id=%s", common.PortalBaseURL, humanID)
	var envInfos []PortalEnvironmentInfo
	resp, err := resty.New().R().
		SetAuthToken(tokenSet.AccessToken). // Set Bearer token for Authorization
		SetResult(&envInfos).               // Unmarshal response into the struct
		Get(url)

	if err != nil {
		return nil, fmt.Errorf("failed to fetch environment details from portal: %w", err)
	}

	if resp.IsError() {
		return nil, fmt.Errorf("failed to fetch environment details from portal: %s", resp.Status())
	}

	if len(envInfos) == 0 {
		return nil, fmt.Errorf("failed to fetch environment details from portal: no such environment")
	}

	if len(envInfos) > 1 {
		return nil, fmt.Errorf("failed to fetch environment details from portal: multiple results returned")
	}

	return &envInfos[0], nil
}

/*
// ResolveTargetEnvironmentFromSlugs resolves the target environment based on organization, project, and environment slugs.
func ResolveTargetEnvironmentFromSlugs(tokens *auth.TokenSet, organization, project, environment string) (*TargetEnvironment, error) {
	portalEnvInfo, err := FetchInfoBySlugs(tokens, organization, project, environment)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch environment info: %w", err)
	}

	if portalEnvInfo.StackDomain == nil || *portalEnvInfo.StackDomain == "" {
		return nil, errors.New("the environment has not been provisioned to any infra stack (stack_domain is empty)")
	}

	stackAPIURL := fmt.Sprintf("https://infra.%s/stackapi", *portalEnvInfo.StackDomain)
	return &TargetEnvironment{
		AccessToken: tokens.AccessToken,
		HumanID:     portalEnvInfo.HumanID,
		StackAPIURL: stackAPIURL,
	}, nil
}

// ResolveTargetEnvironmentHumanID resolves the target environment based on its HumanID.
func ResolveTargetEnvironmentHumanID(tokens *auth.TokenSet, humanID string, stackAPIBaseURLOverride *string) (*TargetEnvironment, error) {
	var stackAPIBaseURL string

	if stackAPIBaseURLOverride != nil && *stackAPIBaseURLOverride != "" {
		stackAPIBaseURL = *stackAPIBaseURLOverride
	} else {
		portalEnvInfo, err := FetchInfoByHumanID(tokens, humanID)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch environment info: %w", err)
		}

		if portalEnvInfo.StackDomain == nil || *portalEnvInfo.StackDomain == "" {
			return nil, errors.New("the environment has not been provisioned to any infra stack (stack_domain is empty)")
		}

		stackAPIBaseURL = fmt.Sprintf("https://infra.%s/stackapi", *portalEnvInfo.StackDomain)
	}

	return &TargetEnvironment{
		AccessToken: tokens.AccessToken,
		HumanID:     humanID,
		StackAPIURL: stackAPIBaseURL,
	}, nil
}
*/
