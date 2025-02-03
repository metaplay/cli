/*
 * Copyright Metaplay. All rights reserved.
 */
package envapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/metahttp"
	"github.com/rs/zerolog/log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"

	"k8s.io/client-go/pkg/apis/clientauthentication"
)

// Wrapper object for accessing an environment within a target stack.
type TargetEnvironment struct {
	TokenSet        *auth.TokenSet   // Tokens to use to access the environment.
	StackApiBaseURL string           // Base URL of the StackAPI, eg, 'https://infra.<stack>/stackapi'
	HumanId         string           // Environment human ID, eg, 'tiny-squids'. Same as Kubernetes namespace.
	HttpClient      *metahttp.Client // HTTP client for the environment.
}

type KubeConfig struct {
	ApiVersion     string                 `yaml:"apiVersion"`
	Clusters       []KubeConfigCluster    `yaml:"clusters"`
	Contexts       []KubeConfigContext    `yaml:"contexts"`
	CurrentContext string                 `yaml:"current-context"`
	Kind           string                 `yaml:"kind"`
	Preferences    map[string]interface{} `yaml:"preferences"`
	Users          []KubeConfigUser       `yaml:"users"`
}

type KubeConfigCluster struct {
	Cluster KubeConfigClusterData `yaml:"cluster"`
	Name    string                `yaml:"name"`
}

type KubeConfigClusterData struct {
	CertificateAuthorityData string `yaml:"certificate-authority-data"`
	Server                   string `yaml:"server"`
}

type KubeConfigContext struct {
	Context KubeConfigContextData `yaml:"context"`
	Name    string                `yaml:"name"`
}

type KubeConfigContextData struct {
	Cluster   string `yaml:"cluster"`
	User      string `yaml:"user"`
	Namespace string `yaml:"namespace"`
}

type KubeConfigUser struct {
	Name string             `yaml:"name"`
	User KubeConfigUserData `yaml:"user"`
}

type KubeConfigUserData struct {
	Token string                 `yaml:"token"`
	Exec  KubeConfigUserDataExec `yaml:"exec"`
}

type KubeConfigUserDataExec struct {
	Command         string   `yaml:"command"`
	Args            []string `yaml:"args"`
	ApiVersion      string   `yaml:"apiVersion"`
	InteractiveMode string   `yaml:"interactiveMode"`
}

type KubeExecCredential struct {
	ApiVersion string                                    `json:"apiVersion"`
	Kind       string                                    `json:"kind"`
	Spec       clientauthentication.ExecCredentialSpec   `json:"spec"`
	Status     clientauthentication.ExecCredentialStatus `json:"status"`
}

// Container for AWS access credentials into the target environment.
type AWSCredentials struct {
	AccessKeyId     string
	SecretAccessKey string
	SessionToken    string
	Expiration      string
}

// Container for access information to an environment's docker registry.
type DockerCredentials struct {
	Username    string
	Password    string
	RegistryURL string
}

func NewTargetEnvironment(tokenSet *auth.TokenSet, stackDomain, humanId string) *TargetEnvironment {
	stackApiBaseURL := fmt.Sprintf("https://infra.%s/stackapi", stackDomain)

	return &TargetEnvironment{
		TokenSet:        tokenSet,
		StackApiBaseURL: stackApiBaseURL,
		HumanId:         humanId,
		HttpClient:      metahttp.NewClient(tokenSet, stackApiBaseURL),
	}
}

// Request details about an environment from the StackAPI.
func (target *TargetEnvironment) GetDetails() (*EnvironmentDetails, error) {
	path := fmt.Sprintf("/v0/deployments/%s", target.HumanId)
	details, err := metahttp.Get[EnvironmentDetails](target.HttpClient, path)
	return &details, err
}

// Get a short-lived kubeconfig with the access credentials embedded in the kubeconfig file.
func (target *TargetEnvironment) GetKubeConfigWithEmbeddedCredentials() (*string, error) {
	log.Debug().Msg("Fetching kubeconfig with embedded secret")
	path := fmt.Sprintf("/v0/credentials/%s/k8s", target.HumanId)
	config, err := metahttp.Post[string](target.HttpClient, path, nil)
	return &config, err
}

// Get the Kubernetes credentials in the execcredential format
func (target *TargetEnvironment) GetKubeExecCredential() (*string, error) {
	path := fmt.Sprintf("/v0/credentials/%s/k8s?type=execcredential", target.HumanId)
	credentials, err := metahttp.Post[string](target.HttpClient, path, nil)
	return &credentials, err
}

/**
* Get a `kubeconfig` payload which invokes `metaplay-auth get-kubernetes-execcredential` to get the actual
* access credentials each time the kubeconfig is used.
* @returns The kubeconfig YAML.
 */
func (target *TargetEnvironment) GetKubeConfigWithExecCredential() (*string, error) {
	path := fmt.Sprintf("/v0/credentials/%s/k8s?type=execcredential", target.HumanId)
	log.Debug().Msgf("Getting Kubernetes KubeConfig with execcredential from %s%s...", target.HttpClient.BaseURL, path)

	credentials, err := metahttp.Post[KubeExecCredential](target.HttpClient, path, nil)
	if err != nil {
		return nil, err
	}

	if string(credentials.Spec.Cluster.CertificateAuthorityData) == "" && credentials.Spec.Cluster.Server == "" {
		return nil, fmt.Errorf("Received kubeExecCredential with missing spec.cluster")
	}

	// TODO: There is probably unnecessary repetition in the error formatting
	userinfo, err := auth.FetchUserInfo(target.TokenSet)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch userinfo: %w", err)
	}

	kubeConfig, err := yaml.Marshal(KubeConfig{
		ApiVersion: "v1",
		Clusters: []KubeConfigCluster{
			{
				Cluster: KubeConfigClusterData{
					CertificateAuthorityData: base64.StdEncoding.EncodeToString(credentials.Spec.Cluster.CertificateAuthorityData[:]),
					Server:                   credentials.Spec.Cluster.Server,
				},
				Name: credentials.Spec.Cluster.Server,
			},
		},
		Contexts: []KubeConfigContext{
			{
				Context: KubeConfigContextData{
					Cluster:   credentials.Spec.Cluster.Server,
					Namespace: target.HumanId,
					User:      userinfo.Email,
				},
				Name: target.HumanId,
			},
		},
		CurrentContext: target.HumanId,
		Kind:           "Config",
		Preferences:    make(map[string]interface{}),
		Users: []KubeConfigUser{
			{
				Name: userinfo.Email,
				User: KubeConfigUserData{
					Exec: KubeConfigUserDataExec{
						Command: "metaplay",
						Args: []string{
							"get",
							"kubernetes-execcredential",
							target.HumanId,
							target.StackApiBaseURL,
						},
						ApiVersion:      "client.authentication.k8s.io/v1beta1",
						InteractiveMode: "Never",
					},
				},
			},
		},
	})
	dump := string(kubeConfig[:])
	return &dump, nil
}

// Get AWS credentials against the target environment.
// \todo migrate this into StackAPI -- AWS creds should not be given to the client
func (target *TargetEnvironment) GetAWSCredentials() (*AWSCredentials, error) {
	path := fmt.Sprintf("/v0/credentials/%s/aws", target.HumanId)
	awsCredentials, err := metahttp.Post[AWSCredentials](target.HttpClient, path, nil)
	if err != nil {
		return nil, err
	}
	if awsCredentials.AccessKeyId == "" {
		return nil, fmt.Errorf("AWS credentials missing AccessKeyId")
	}
	if awsCredentials.SecretAccessKey == "" {
		return nil, fmt.Errorf("AWS credential missing SecretAccessKey")
	}
	return &awsCredentials, err
}

// Get Docker credentials for the environment's docker registry.
func (target *TargetEnvironment) GetDockerCredentials(envDetails *EnvironmentDetails) (*DockerCredentials, error) {
	// Fetch AWS credentials from Metaplay cloud
	log.Debug().Msg("Get AWS credentials")
	awsCredentials, err := target.GetAWSCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to get AWS credentials: %v", err)
	}

	// Create AWS config with provided region and credentials
	log.Debug().Msg("Create AWS config")
	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(envDetails.Deployment.AwsRegion),
		config.WithCredentialsProvider(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     awsCredentials.AccessKeyId,
				SecretAccessKey: awsCredentials.SecretAccessKey,
				SessionToken:    awsCredentials.SessionToken,
			}, nil
		})),
	)
	if err != nil {
		return nil, err
	}

	// Create an ECR client
	log.Debug().Msg("Create ECR client")
	client := ecr.NewFromConfig(cfg)

	// Fetch the ECR docker authentication token
	log.Debug().Msg("Fetch ECR login credentials from AWS")
	response, err := client.GetAuthorizationToken(context.TODO(), &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return nil, err
	}

	if len(response.AuthorizationData) == 0 ||
		response.AuthorizationData[0].AuthorizationToken == nil ||
		response.AuthorizationData[0].ProxyEndpoint == nil {
		return nil, errors.New("received an empty authorization token response for ECR repository")
	}

	// Parse username and password from the response (separated by a ':')
	log.Debug().Msg("Parse ECR response")
	registryURL := *response.AuthorizationData[0].ProxyEndpoint
	authorization64 := *response.AuthorizationData[0].AuthorizationToken
	decoded, err := base64.StdEncoding.DecodeString(authorization64)
	if err != nil {
		return nil, err
	}

	authorization := string(decoded)
	parts := strings.SplitN(authorization, ":", 2)
	if len(parts) != 2 {
		return nil, errors.New("failed to parse authorization token")
	}
	username := parts[0]
	password := parts[1]

	log.Debug().Msgf("ECR: username=%s, proxyEndpoint=%s", username, registryURL)

	return &DockerCredentials{
		Username:    username,
		Password:    password,
		RegistryURL: registryURL,
	}, nil
}
