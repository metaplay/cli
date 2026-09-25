/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/metahttp"
	"github.com/rs/zerolog/log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	clientauthenticationv1beta1 "k8s.io/client-go/pkg/apis/clientauthentication/v1beta1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Wrapper object for accessing an environment within a target stack.
type TargetEnvironment struct {
	TokenSet        *auth.TokenSet   // Tokens to use to access the environment.
	StackApiBaseURL string           // Base URL of the StackAPI, eg, 'https://infra.<stack>/stackapi'
	HumanID         string           // Environment human ID, eg, 'lovely-wombats-build-nimbly'. Same as Kubernetes namespace.
	StackApiClient  *metahttp.Client // HTTP client to access environment StackAPI.

	primaryKubeClient *KubeClient       // Lazily initialized KubeClient.
	targetGameServer  *TargetGameServer // Lazily initialized TargetGameServer.
}

// Container for AWS access credentials into the target environment.
// The JSON names match those used by AWS.
type AWSCredentials struct {
	Version         int    `json:"Version"`
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

// Container for access information to an environment's docker registry.
type DockerCredentials struct {
	Username string
	Password string
	// The registry, in one of two shapes: ECR's proxy endpoint, scheme and all
	// ('https://<id>.dkr.ecr...'), or the bare host a stack that issues its own
	// credentials reports. Not a name a registry client will parse.
	RegistryURL string
}

func NewTargetEnvironment(tokenSet *auth.TokenSet, stackDomain, humanID string) *TargetEnvironment {
	return NewTargetEnvironmentAtStackAPI(tokenSet, fmt.Sprintf("https://infra.%s/stackapi", stackDomain), humanID)
}

// NewTargetEnvironmentAtStackAPI is NewTargetEnvironment for a caller that
// already holds the StackAPI base URL and would otherwise have to take it apart
// to recover a stack domain this type never stores.
func NewTargetEnvironmentAtStackAPI(tokenSet *auth.TokenSet, stackApiBaseURL, humanID string) *TargetEnvironment {
	log.Debug().Msgf("Create TargetEnvironment with stackApiBaseURL=%s", stackApiBaseURL)
	return &TargetEnvironment{
		TokenSet:        tokenSet,
		StackApiBaseURL: stackApiBaseURL,
		HumanID:         humanID,
		StackApiClient:  metahttp.NewJSONClient(tokenSet, stackApiBaseURL),
	}
}

func (target *TargetEnvironment) GetKubernetesNamespace() string {
	return target.HumanID
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

	// Create REST client.
	restClient, err := rest.RESTClientFor(config)
	if err != nil {
		return nil, err
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
		Namespace:       target.HumanID,
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
		Namespace:       target.HumanID,
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

	return nil, fmt.Errorf("neither old nor new gameserver CR found in Kubernetes")
}

// Request details about an environment from the StackAPI.
func (target *TargetEnvironment) GetDetails() (*DeploymentSecret, error) {
	path := fmt.Sprintf("/v0/deployments/%s", target.HumanID)
	log.Debug().Msgf("Get environment details from %s%s", target.StackApiClient.BaseURL, path)
	details, err := metahttp.Get[DeploymentSecret](target.StackApiClient, path)
	return &details, err
}

// Get a short-lived kubeconfig with the access credentials embedded in the kubeconfig file.
func (target *TargetEnvironment) GetKubeConfigWithEmbeddedCredentials() (string, error) {
	log.Debug().Msg("Fetching kubeconfig with embedded secret")
	path := fmt.Sprintf("/v0/credentials/%s/k8s", target.HumanID)
	config, err := metahttp.Post[string](target.StackApiClient, path, nil, "")
	return config, err
}

// Get the Kubernetes credentials in the execcredential format
func (target *TargetEnvironment) GetKubeExecCredential() (*string, error) {
	path := fmt.Sprintf("/v0/credentials/%s/k8s?type=execcredential", target.HumanID)
	credentials, err := metahttp.Post[string](target.StackApiClient, path, nil, "")
	return &credentials, err
}

// ProxyExecCredentialSkew is how much earlier than its access token an exec
// credential for the Kubernetes API proxy reports expiring, so that kubectl
// never presents an expired token. The CLI refreshes on the same boundary.
const ProxyExecCredentialSkew = time.Minute

// NewProxyExecCredential returns the exec credential for a kubeconfig pointing
// at the Kubernetes API proxy: the access token of the session with
// authProvider, refreshed first if it expires within ProxyExecCredentialSkew.
func NewProxyExecCredential(authProvider *auth.AuthProviderConfig) (string, error) {
	// kubectl runs the plugin without a terminal, so a missing session cannot
	// be logged in to here.
	tokenSet, err := auth.LoadAndRefreshTokenSetValidFor(authProvider, ProxyExecCredentialSkew)
	if err != nil {
		return "", err
	}
	if tokenSet == nil {
		return "", clierrors.New("Not logged in").
			WithSuggestion("Run '" + authProvider.LoginCommand() + "' and try again")
	}
	expiresAt, err := auth.AccessTokenExpiresAt(tokenSet)
	if err != nil {
		return "", clierrors.Wrap(err, "Failed to parse access token expiration").
			WithSuggestion("Run '" + authProvider.LoginCommand() + "' to re-authenticate")
	}

	// A token already within the skew, handed on because it could not be
	// refreshed or because tokens live no longer than the skew, is reported
	// with its own expiry. One already past would have kubectl run the plugin
	// again for every request.
	expiry := metav1.NewTime(expiresAt.Add(-ProxyExecCredentialSkew))
	if !expiry.After(time.Now()) {
		expiry = metav1.NewTime(expiresAt)
	}
	payload, err := json.Marshal(clientauthenticationv1beta1.ExecCredential{
		TypeMeta: metav1.TypeMeta{APIVersion: "client.authentication.k8s.io/v1beta1", Kind: "ExecCredential"},
		Status:   &clientauthenticationv1beta1.ExecCredentialStatus{Token: tokenSet.AccessToken, ExpirationTimestamp: &expiry},
	})
	if err != nil {
		return "", clierrors.Wrap(err, "Failed to encode the exec credential")
	}
	return string(payload), nil
}

// ErrKubernetesAPIProxyRefused is wrapped by the errors GetKubeConfigWithExecCredential
// returns when the stack serves the Kubernetes API proxy but a dynamic kubeconfig
// cannot safely point at it. A static kubeconfig carries the stack's own
// credential and is unaffected.
var ErrKubernetesAPIProxyRefused = errors.New("a dynamic kubeconfig cannot use the stack's Kubernetes API proxy")

// GetKubeConfigWithExecCredential returns a kubeconfig that runs the CLI for a
// credential each time kubectl needs one. userID names its user, and is not
// used otherwise. A stack serving the Kubernetes API proxy names a server under
// StackAPI in its kubeconfig, and the CLI then answers kubectl with its own
// access token. Otherwise the CLI asks StackAPI for a credential.
//
// The proxy plugin answers with the default auth provider's token, so
// usesDefaultAuthProvider must say whether the environment signs in with it.
// An environment that does not is refused rather than have another
// provider's token handed to its stack.
func (target *TargetEnvironment) GetKubeConfigWithExecCredential(userID string, usesDefaultAuthProvider bool) (string, error) {
	log.Debug().Msgf("Getting the environment's kubeconfig from %s to find its Kubernetes API", target.StackApiBaseURL)
	served, err := target.GetKubeConfigWithEmbeddedCredentials()
	if err != nil {
		return "", err
	}
	server, certificateAuthority, err := kubeconfigCluster(served)
	if err != nil {
		return "", err
	}

	isProxy, err := target.isKubernetesAPIProxy(server)
	if err != nil {
		return "", err
	}
	pluginArgs := []string{"get", "kubernetes-execcredential", target.HumanID}
	if isProxy {
		if !usesDefaultAuthProvider {
			return "", fmt.Errorf("%w: the environment uses an auth provider other than the default, whose token the proxy plugin answers with", ErrKubernetesAPIProxyRefused)
		}
		pluginArgs = append(pluginArgs, "--proxy")
	} else {
		pluginArgs = append(pluginArgs, target.StackApiBaseURL)
	}

	kubeConfig, err := yaml.Marshal(KubeConfig{
		ApiVersion: "v1",
		Clusters: []KubeConfigCluster{
			{
				Cluster: KubeConfigClusterData{
					CertificateAuthorityData: base64.StdEncoding.EncodeToString(certificateAuthority),
					Server:                   server,
				},
				Name: server,
			},
		},
		Contexts: []KubeConfigContext{
			{
				Context: KubeConfigContextData{
					Cluster:   server,
					Namespace: target.HumanID,
					User:      userID,
				},
				Name: target.HumanID,
			},
		},
		CurrentContext: target.HumanID,
		Kind:           "Config",
		Preferences:    make(map[string]any),
		Users: []KubeConfigUser{
			{
				Name: userID,
				User: KubeConfigUserData{
					Exec: KubeConfigUserDataExec{
						Command:         "metaplay",
						Args:            pluginArgs,
						ApiVersion:      "client.authentication.k8s.io/v1beta1",
						InteractiveMode: "Never",
					},
				},
			},
		},
	})
	if err != nil {
		return "", err
	}
	return string(kubeConfig), nil
}

// isKubernetesAPIProxy reports whether server is the Kubernetes API proxy,
// which a stack names under StackAPI's own URL. A server under StackAPI's path
// on another host is refused rather than taken for a cluster: the CLI sends
// its access token only to the StackAPI it already talks to.
func (target *TargetEnvironment) isKubernetesAPIProxy(server string) (bool, error) {
	serverURL, err := url.Parse(server)
	if err != nil {
		return false, fmt.Errorf("the environment's kubeconfig names an invalid server %q: %w", server, err)
	}
	stackAPIURL, err := url.Parse(target.StackApiBaseURL)
	if err != nil {
		return false, fmt.Errorf("invalid StackAPI base URL %q: %w", target.StackApiBaseURL, err)
	}
	// A base URL spelled with a trailing slash would otherwise demand a double
	// slash, and take the proxy for a cluster.
	if !strings.HasPrefix(serverURL.Path, strings.TrimSuffix(stackAPIURL.Path, "/")+"/") {
		return false, nil
	}
	if serverURL.Scheme != stackAPIURL.Scheme || !sameHostAndPort(serverURL, stackAPIURL) {
		return false, fmt.Errorf("%w: the environment's kubeconfig names StackAPI at %s://%s, but the CLI reached it at %s://%s",
			ErrKubernetesAPIProxyRefused, serverURL.Scheme, serverURL.Host, stackAPIURL.Scheme, stackAPIURL.Host)
	}
	return true, nil
}

// sameHostAndPort reports whether two URLs of one scheme name the same host,
// case-insensitively, and the same port, reading an omitted one as the
// scheme's default.
func sameHostAndPort(a, b *url.URL) bool {
	return strings.EqualFold(a.Hostname(), b.Hostname()) && portOrDefault(a) == portOrDefault(b)
}

func portOrDefault(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch u.Scheme {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// kubeconfigCluster returns the server and certificate authority a kubeconfig's
// current context names. An absent certificate authority is not an error: a
// server with a publicly trusted certificate needs none.
func kubeconfigCluster(payload string) (string, []byte, error) {
	config, err := clientcmd.Load([]byte(payload))
	if err != nil {
		return "", nil, fmt.Errorf("the environment's kubeconfig does not parse: %w", err)
	}
	kubeContext, ok := config.Contexts[config.CurrentContext]
	if !ok {
		return "", nil, fmt.Errorf("the environment's kubeconfig has no current context %q", config.CurrentContext)
	}
	cluster, ok := config.Clusters[kubeContext.Cluster]
	if !ok || cluster.Server == "" {
		return "", nil, fmt.Errorf("the environment's kubeconfig names no server for its context %q", config.CurrentContext)
	}
	return cluster.Server, cluster.CertificateAuthorityData, nil
}

// Get AWS credentials against the target environment.
// \todo migrate this into StackAPI -- AWS creds should not be given to the client
func (target *TargetEnvironment) GetAWSCredentials() (*AWSCredentials, error) {
	path := fmt.Sprintf("/v0/credentials/%s/aws", target.HumanID)
	awsCredentials, err := metahttp.Post[AWSCredentials](target.StackApiClient, path, nil, "")
	if err != nil {
		return nil, err
	}
	if awsCredentials.AccessKeyID == "" {
		return nil, fmt.Errorf("AWS credentials missing AccessKeyId")
	}
	if awsCredentials.SecretAccessKey == "" {
		return nil, fmt.Errorf("AWS credential missing SecretAccessKey")
	}

	awsCredentials.Version = 1

	return &awsCredentials, err
}

// newECRClient creates an authenticated ECR client for the environment.
func (target *TargetEnvironment) newECRClient(envDetails *DeploymentSecret) (*ecr.Client, error) {
	log.Debug().Msg("Get AWS credentials")
	awsCredentials, err := target.GetAWSCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to get AWS credentials: %w", err)
	}

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

	log.Debug().Msg("Create ECR client")
	return ecr.NewFromConfig(cfg), nil
}

// Get Docker credentials for the environment's docker registry.
func (target *TargetEnvironment) GetDockerCredentials(envDetails *DeploymentSecret) (*DockerCredentials, error) {
	client, err := target.newECRClient(envDetails)
	if err != nil {
		return nil, err
	}

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
	username, password, ok := strings.Cut(authorization, ":")
	if !ok {
		return nil, errors.New("failed to parse authorization token")
	}

	log.Debug().Msgf("ECR: username=%s, proxyEndpoint=%s", username, registryURL)

	return &DockerCredentials{
		Username:    username,
		Password:    password,
		RegistryURL: registryURL,
	}, nil
}

// ECRImage represents a single image in the ECR repository.
type ECRImage struct {
	Tags      []string  `json:"tags"`
	PushedAt  time.Time `json:"pushedAt"`
	SizeBytes int64     `json:"sizeBytes"`
	Digest    string    `json:"digest"`
}

// ListECRImages lists Docker images in the environment's ECR repository.
// When maxResults > 0, stops fetching pages once at least that many tagged images
// have been collected (the caller should trim if an exact limit is needed). When 0, fetches all.
func (target *TargetEnvironment) ListECRImages(envDetails *DeploymentSecret, maxResults int) ([]ECRImage, error) {
	client, err := target.newECRClient(envDetails)
	if err != nil {
		return nil, err
	}

	// Extract repository name from full URI (strip registry prefix).
	// EcrRepo looks like "123456789.dkr.ecr.us-west-2.amazonaws.com/myrepo"
	repoName := envDetails.Deployment.EcrRepo
	if idx := strings.Index(repoName, "/"); idx != -1 {
		repoName = repoName[idx+1:]
	}

	var images []ECRImage
	var nextToken *string
	for {
		input := &ecr.DescribeImagesInput{
			RepositoryName: &repoName,
			NextToken:      nextToken,
		}

		output, err := client.DescribeImages(context.TODO(), input)
		if err != nil {
			return nil, fmt.Errorf("failed to list images from ECR: %w", err)
		}

		for _, detail := range output.ImageDetails {
			// Skip untagged images (old layers)
			if len(detail.ImageTags) == 0 {
				continue
			}

			var pushedAt time.Time
			if detail.ImagePushedAt != nil {
				pushedAt = *detail.ImagePushedAt
			}
			var sizeBytes int64
			if detail.ImageSizeInBytes != nil {
				sizeBytes = *detail.ImageSizeInBytes
			}
			digest := ""
			if detail.ImageDigest != nil {
				digest = *detail.ImageDigest
			}

			images = append(images, ECRImage{
				Tags:      detail.ImageTags,
				PushedAt:  pushedAt,
				SizeBytes: sizeBytes,
				Digest:    digest,
			})
		}

		nextToken = output.NextToken
		if nextToken == nil {
			break
		}

		// Stop fetching if we have enough images
		if maxResults > 0 && len(images) >= maxResults {
			break
		}
	}

	return images, nil
}
