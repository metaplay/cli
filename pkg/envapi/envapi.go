/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

// \todo Copy-pasted from StackAPI

// Kubernetes secret `metaplay-deployment` field 'deployment'.
type DeploymentSecret struct {
	Deployment    Deployment    `json:"deployment"`
	Format        string        `json:"format"`
	OAuth2Client  OAuth2Client  `json:"oauth2_client"`
	Observability Observability `json:"observability"`
	Type          string        `json:"type"`
}

type Deployment struct {
	AdminHostname            string `json:"admin_hostname"`
	AdminTLSCert             string `json:"admin_tls_cert"`
	AwsRegion                string `json:"aws_region"`
	CdnDistributionArn       string `json:"cdn_distribution_arn"`
	CdnDistributionID        string `json:"cdn_distribution_id"`
	CdnS3Fqdn                string `json:"cdn_s3_fqdn"`
	EcrRepo                  string `json:"ecr_repo"`
	GameserverAdminIamRole   string `json:"gameserver_admin_iam_role"`
	GameserverIamRole        string `json:"gameserver_iam_role"`
	GameserverServiceAccount string `json:"gameserver_service_account"`
	KubernetesNamespace      string `json:"kubernetes_namespace"`
	MetaplayInfraVersion     string `json:"metaplay_infra_version"`
	S3BucketPrivate          string `json:"s3_bucket_private"`
	S3BucketPublic           string `json:"s3_bucket_public"`
	ServerHostname           string `json:"server_hostname"`
	ServerPorts              []int  `json:"server_ports"`
	ServerTLSCert            string `json:"server_tls_cert"`

	// Deprecated fields -- still included in the response but should not be used!
	// MetaplayRequiredSdkVersion     string   `json:"metaplay_required_sdk_version"`
	// MetaplaySupportedChartVersions []string `json:"metaplay_supported_chart_versions"`
	// TenantEnvironment              string   `json:"tenant_environment"`
	// TenantOrganization             string   `json:"tenant_organization"`
	// TenantProject                  string   `json:"tenant_project"`
}

type OAuth2Client struct {
	AuthProvider      string   `json:"auth_provider"`
	Audience          string   `json:"audience"`
	ClientID          string   `json:"client_id"`
	ClientSecret      string   `json:"client_secret"`
	Domain            string   `json:"domain"`
	EmailDomain       string   `json:"email_domain"`
	Issuer            string   `json:"issuer"`
	LogoutRedirectUri string   `json:"logout_redirect_uri"`
	RolesClaim        string   `json:"roles_claim"`
	RolePrefix        string   `json:"role_prefix"`
	LocalCallback     string   `json:"local_callback"`
	AdditionalScopes  []string `json:"additional_scopes"`
}

type Observability struct {
	LokiEndpoint       string `json:"loki_endpoint"`
	LokiPassword       string `json:"loki_password"`
	LokiUsername       string `json:"loki_username"`
	PrometheusEndpoint string `json:"prometheus_endpoint"`
	PrometheusPassword string `json:"prometheus_password"`
	PrometheusUsername string `json:"prometheus_username"`
}
