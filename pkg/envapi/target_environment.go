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

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Wrapper object for accessing an environment within a target stack.
type TargetEnvironment struct {
	TokenSet        *auth.TokenSet   // Tokens to use to access the environment.
	StackApiBaseURL string           // Base URL of the StackAPI, eg, 'https://infra.<stack>/stackapi'
	HumanId         string           // Environment human ID, eg, 'tiny-squids'. Same as Kubernetes namespace.
	StackApiClient  *metahttp.Client // HTTP client to access environment StackAPI.

	primaryKubeClient *KubeClient       // Lazily initialized KubeClient.
	targetGameServer  *TargetGameServer // Lazily initialized TargetGameServer.
}

// Container for AWS access credentials into the target environment.
// The JSON names match those used by AWS.
type AWSCredentials struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
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
		StackApiClient:  metahttp.NewClient(tokenSet, stackApiBaseURL),
	}
}

func (target *TargetEnvironment) GetKubernetesNamespace() string {
	return target.HumanId
}

// Get a Kubernetes client for the primary cluster.
func (target *TargetEnvironment) GetPrimaryKubeClient() (*KubeClient, error) {
	// If already created, just return the earlier instance.
	if target.primaryKubeClient != nil {
		return target.primaryKubeClient, nil
	}

	// Initialize RestConfig when creating a new target environment
	kubeconfig, err := target.GetKubeConfigWithEmbeddedCredentials()
	if err != nil {
		return nil, err
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes REST config from kubeconfig")
	}

	// Create a new scheme and codec factory
	scheme := runtime.NewScheme()
	codecs := serializer.NewCodecFactory(scheme)

	// Create REST client config
	config := rest.CopyConfig(restConfig)
	config.APIPath = "/api"
	config.GroupVersion = &schema.GroupVersion{Group: "", Version: "v1"}
	config.NegotiatedSerializer = codecs.WithoutConversion()

	// Create RESTClient.
	restClient, err := rest.RESTClientFor(config)
	if err != nil {
		fmt.Errorf("failed to create Kubernetes REST client: %w", err)
	}

	// Create the Kubernetes static clientset.
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	// Create a Kubernetes DynamicClient.
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic Kubernetes client: %w", err)
	}

	// Create and store the KubeClient for primary cluster.
	target.primaryKubeClient = &KubeClient{
		Namespace:     target.GetKubernetesNamespace(),
		KubeConfig:    kubeconfig,
		RestConfig:    restConfig,
		RestClient:    restClient,
		Clientset:     clientset,
		DynamicClient: dynamicClient,
	}

	return target.primaryKubeClient, nil
}

func (target *TargetEnvironment) tryGetGameServerNewCR(ctx context.Context, kubeCli *KubeClient) (*TargetGameServer, error) {
	// Try to get the gameserver CR used by the new operator.
	newGameServerCR, err := getGameServerNewCR(ctx, kubeCli)
	if err != nil {
		return nil, err
	}
	if newGameServerCR == nil {
		return nil, nil
	}

	// Resolve all clusters.
	primaryCluster := &TargetCluster{
		KubeClient: kubeCli,
	}

	// Resolve all clusters.
	clusters := []TargetCluster{
		*primaryCluster,
		// \todo properly resolve edge clusters
	}

	// Find all shard sets belonging to this gameserver.
	// \todo check that this is correct
	shardSets := []TargetShardSet{}
	for _, spec := range newGameServerCR.Spec.Shards {
		shardSets = append(shardSets, TargetShardSet{
			Name:    spec.Name,
			Cluster: primaryCluster, // \todo handle other clusters
		})
	}

	// Create and store gameserver CR wrapper instance.
	log.Debug().Msgf("Found new gameserver CR: name=%s, resourceVersion=%s, UID=%s", newGameServerCR.GetName(), newGameServerCR.GetResourceVersion(), newGameServerCR.GetUID())
	return &TargetGameServer{
		Namespace:       target.HumanId,
		GameServerNewCR: newGameServerCR,
		Clusters:        clusters,
		ShardSets:       shardSets,
	}, nil
}

func (target *TargetEnvironment) tryGetGameServerOldCR(ctx context.Context, kubeCli *KubeClient) (*TargetGameServer, error) {
	// If new operator CR not found, assume we have a old CR.
	log.Debug().Msgf("... new gameserver CR not found; assume old operator")
	gameserverCR, err := getGameServerOldCR(ctx, kubeCli)
	if err != nil {
		return nil, err
	}
	if gameserverCR == nil {
		return nil, nil
	}

	// Only primary cluster supported with old operator.
	clusters := []TargetCluster{
		{
			KubeClient: kubeCli,
		},
	}

	// Find all shard sets belonging to this gameserver.
	// With old operator, all shard sets are on the primary cluster.
	// \todo check that this is correct
	shardSets := []TargetShardSet{}
	for _, spec := range gameserverCR.Spec.ShardSpec {
		shardSets = append(shardSets, TargetShardSet{
			Name:    spec.Name,
			Cluster: &clusters[0], // only primary cluster is supported
		})
	}

	// Create and store gameserver CR wrapper instance.
	return &TargetGameServer{
		Namespace:       target.HumanId,
		GameServerOldCR: gameserverCR,
		Clusters:        clusters,
		ShardSets:       shardSets,
	}, nil
}

// Get the accessor to the gameserver resource in this environment.
func (target *TargetEnvironment) GetGameServer(ctx context.Context) (*TargetGameServer, error) {
	// If already created, return the instance.
	if target.targetGameServer != nil {
		return target.targetGameServer, nil
	}

	// Get primary Kubernetes client.
	kubeCli, err := target.GetPrimaryKubeClient()
	if err != nil {
		return nil, err
	}

	// Try to resolve the gameserver with new CR.
	newGameServer, err := target.tryGetGameServerNewCR(ctx, kubeCli)
	if err != nil {
		return nil, err
	}
	if newGameServer != nil {
		target.targetGameServer = newGameServer
		return newGameServer, nil
	}

	// Try to resolve the gameserver with old CR.
	oldGameServer, err := target.tryGetGameServerOldCR(ctx, kubeCli)
	if err != nil {
		return nil, err
	}
	if oldGameServer != nil {
		target.targetGameServer = oldGameServer
		return oldGameServer, nil
	}

	return nil, fmt.Errorf("Neither old nor new gameserver CR found in Kubernetes")
}

// Request details about an environment from the StackAPI.
func (target *TargetEnvironment) GetDetails() (*EnvironmentDetails, error) {
	path := fmt.Sprintf("/v0/deployments/%s", target.HumanId)
	details, err := metahttp.Get[EnvironmentDetails](target.StackApiClient, path)
	return &details, err
}

// Get a short-lived kubeconfig with the access credentials embedded in the kubeconfig file.
func (target *TargetEnvironment) GetKubeConfigWithEmbeddedCredentials() (string, error) {
	log.Debug().Msg("Fetching kubeconfig with embedded secret")
	path := fmt.Sprintf("/v0/credentials/%s/k8s", target.HumanId)
	config, err := metahttp.Post[string](target.StackApiClient, path, nil)
	return config, err
}

// Get the Kubernetes credentials in the execcredential format
func (target *TargetEnvironment) GetKubeExecCredential() (*string, error) {
	path := fmt.Sprintf("/v0/credentials/%s/k8s?type=execcredential", target.HumanId)
	credentials, err := metahttp.Post[string](target.StackApiClient, path, nil)
	return &credentials, err
}

/**
* Get a `kubeconfig` payload which invokes `metaplay-auth get-kubernetes-execcredential` to get the actual
* access credentials each time the kubeconfig is used.
* @returns The kubeconfig YAML.
 */
func (target *TargetEnvironment) GetKubeConfigWithExecCredential() (string, error) {
	path := fmt.Sprintf("/v0/credentials/%s/k8s?type=execcredential", target.HumanId)
	log.Debug().Msgf("Getting Kubernetes KubeConfig with execcredential from %s%s...", target.StackApiClient.BaseURL, path)

	credentials, err := metahttp.Post[KubeExecCredential](target.StackApiClient, path, nil)
	if err != nil {
		return "", err
	}

	if string(credentials.Spec.Cluster.CertificateAuthorityData) == "" && credentials.Spec.Cluster.Server == "" {
		return "", fmt.Errorf("Received kubeExecCredential with missing spec.cluster")
	}

	// TODO: There is probably unnecessary repetition in the error formatting
	userinfo, err := auth.FetchUserInfo(target.TokenSet)
	if err != nil {
		return "", fmt.Errorf("Failed to fetch userinfo: %w", err)
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
	return dump, nil
}

// Get AWS credentials against the target environment.
// \todo migrate this into StackAPI -- AWS creds should not be given to the client
func (target *TargetEnvironment) GetAWSCredentials() (*AWSCredentials, error) {
	path := fmt.Sprintf("/v0/credentials/%s/aws", target.HumanId)
	awsCredentials, err := metahttp.Post[AWSCredentials](target.StackApiClient, path, nil)
	if err != nil {
		return nil, err
	}
	if awsCredentials.AccessKeyID == "" {
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
				AccessKeyID:     awsCredentials.AccessKeyID,
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
