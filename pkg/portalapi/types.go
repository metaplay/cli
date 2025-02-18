/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package portalapi

import (
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/metahttp"
)

// Type (tier) of project.
type ProjectTier string

const (
	ProjectTierFree         ProjectTier = "free"
	ProjectTierPreLaunch    ProjectTier = "pre-launch"
	ProjectTierProduction   ProjectTier = "production"
	ProjectTierPrivateCloud ProjectTier = "private-cloud"
)

// Project support tier.
type SupportTier string

const (
	ProjectSupportTierCommunity       SupportTier = "community"
	ProjectSupportTierNextBusinessDay SupportTier = "next-business-day"
	ProjectSupportTier24_7            SupportTier = "24-7"
	ProjectSupportTierTailored        SupportTier = "tailored"
	ProjectSupportTierInternalTesting SupportTier = "testing-internal-only"
)

// Environment type (as specified by the portal).
type EnvironmentType string

const (
	EnvironmentTypeDevelopment EnvironmentType = "development"
	EnvironmentTypeStaging     EnvironmentType = "staging"
	EnvironmentTypeProduction  EnvironmentType = "production"
)

// Client represents a Portal API client that handles authentication and requests.
type Client struct {
	httpClient *metahttp.Client
	baseURL    string
	tokenSet   *auth.TokenSet
}

// Type returned by GET /api/v1/organizations/user-organizations.
type OrganizationWithProjects struct {
	UUID      string        `json:"id"`         // UUID of the organization.
	Name      string        `json:"name"`       // Name of the organization.
	Slug      string        `json:"slug"`       // Slugified name of the organization.
	CreatedAt string        `json:"created_at"` // Timestamp when organization was created.
	Role      string        `json:"role"`       // User's role in the organization.
	Projects  []ProjectInfo `json:"projects"`   // Accessible projects within the organization.
}

// ProjectInfo represents information about a project received from the portal.
type ProjectInfo struct {
	UUID             string      `json:"id"`               // UUID of the project.
	OrganizationUUID string      `json:"organization_id"`  // Owner organization UUID.
	HumanID          string      `json:"human_id"`         // Immutable human-readable identified, eg, 'gorgeous-bear'.
	Name             string      `json:"name"`             // Name of the project.
	Slug             string      `json:"slug"`             // Slugified name of the project.
	CreatedAt        string      `json:"created_at"`       // Timestamp when the project was created.
	MaxDevEnvs       int         `json:"max_dev_envs"`     // Maximum number of development environments allowed.
	MaxProdEnvs      int         `json:"max_prod_envs"`    // Maximum number of production environments allowed.
	MaxStagingEnvs   int         `json:"max_staging_envs"` // Maximum number of staging environments allowed.
	SupportTier      SupportTier `json:"support_tier"`     // Support tier for the project (e.g., 'community').
	Type             ProjectTier `json:"type"`             // Type of project (e.g., 'free').
}

// EnvironmentInfo represents information about an environment received from the portal.
type EnvironmentInfo struct {
	UID         string          `json:"id"`           // UUID of the environment
	ProjectUID  string          `json:"project_id"`   // UUID of the project that this environment belongs to
	Name        string          `json:"name"`         // User-provided name for the environment (can change)
	URL         string          `json:"url"`          // TODO: What is this URL?
	Slug        string          `json:"slug"`         // Slug for the environment (simplified version of name)
	CreatedAt   string          `json:"created_at"`   // Creation time of the environment (ISO8601 string)
	Type        EnvironmentType `json:"type"`         // Type of the environment (e.g., 'development' or 'production')
	HumanID     string          `json:"human_id"`     // Immutable human-readable identifier, eg, 'tiny-squids'
	EnvDomain   string          `json:"env_domain"`   // Domain that the environment uses
	StackDomain string          `json:"stack_domain"` // Domain of the infra stack
}
