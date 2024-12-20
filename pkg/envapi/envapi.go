package envapi

// \todo Copy-pasted from StackAPI

// `deployment` K8s secret's format
type EnvironmentDetails struct {
	Deployment    Deployment    `json:"deployment"`
	Format        string        `json:"format"`
	OAuth2Client  OAuth2Client  `json:"oauth2_client"`
	Observability Observability `json:"observability"`
	Type          string        `json:"type"`
}

type Deployment struct {
	AdminHostname                  string   `json:"admin_hostname"`
	AdminTlsCert                   string   `json:"admin_tls_cert"`
	AwsRegion                      string   `json:"aws_region"`
	CdnDistributionArn             string   `json:"cdn_distribution_arn"`
	CdnDistributionId              string   `json:"cdn_distribution_id"`
	CdnS3Fqdn                      string   `json:"cdn_s3_fqdn"`
	EcrRepo                        string   `json:"ecr_repo"`
	GameserverAdminIamRole         string   `json:"gameserver_admin_iam_role"`
	GameserverIamRole              string   `json:"gameserver_iam_role"`
	GameserverServiceAccount       string   `json:"gameserver_service_account"`
	KubernetesNamespace            string   `json:"kubernetes_namespace"`
	MetaplayInfraVersion           string   `json:"metaplay_infra_version"`
	MetaplayRequiredSdkVersion     string   `json:"metaplay_required_sdk_version"`
	MetaplaySupportedChartVersions []string `json:"metaplay_supported_chart_versions"`
	S3BucketPrivate                string   `json:"s3_bucket_private"`
	S3BucketPublic                 string   `json:"s3_bucket_public"`
	ServerHostname                 string   `json:"server_hostname"`
	ServerPorts                    []int    `json:"server_ports"`
	ServerTlsCert                  string   `json:"server_tls_cert"`
	TenantEnvironment              string   `json:"tenant_environment"`
	TenantOrganization             string   `json:"tenant_organization"`
	TenantProject                  string   `json:"tenant_project"`
}

type OAuth2Client struct {
	Audience          string `json:"audience"`
	ClientId          string `json:"client_id"`
	ClientSecret      string `json:"client_secret"`
	Domain            string `json:"domain"`
	EmailDomain       string `json:"email_domain"`
	Issuer            string `json:"issuer"`
	LogoutRedirectUri string `json:"logout_redirect_uri"`
	RolesClaim        string `json:"roles_claim"`
	LocalCallback     string `json:"local_callback"`
}

type Observability struct {
	LokiEndpoint       string `json:"loki_endpoint"`
	LokiPassword       string `json:"loki_password"`
	LokiUsername       string `json:"loki_username"`
	PrometheusEndpoint string `json:"prometheus_endpoint"`
	PrometheusPassword string `json:"prometheus_password"`
	PrometheusUsername string `json:"prometheus_username"`
}
