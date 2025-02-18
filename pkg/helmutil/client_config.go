/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package helmutil

import (
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"helm.sh/helm/v3/pkg/action"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// Initialize a new Helm "ActionConfig" which contains the kubeconfig used to make
// the Helm operations.
func NewActionConfig(kubeconfigPayload string, namespace string) (*action.Configuration, error) {
	// Parse kubeconfig payload
	clientConfig, err := clientcmd.NewClientConfigFromBytes([]byte(kubeconfigPayload))
	if err != nil {
		return nil, err
	}

	// Create RESTClientGetter
	restGetter := &clientConfigRESTClientGetter{
		config: clientConfig,
	}

	// Initialize Helm action configuration
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(restGetter, namespace, "secret", log.Printf); err != nil {
		return nil, fmt.Errorf("failed to initialize Helm configuration: %w", err)
	}

	return actionConfig, nil
}

// Custom RESTClientGetter to allow passing in-memory kubeconfig to Helm.
// Who comes up with these APIs ?!
type clientConfigRESTClientGetter struct {
	config  clientcmd.ClientConfig
	timeout time.Duration
}

func (c *clientConfigRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	restConfig, err := c.config.ClientConfig()
	if err != nil {
		return nil, err
	}

	// Apply the custom timeout to the REST config
	restConfig.Timeout = c.timeout

	return restConfig, nil
}

func (c *clientConfigRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	restConfig, err := c.config.ClientConfig()
	if err != nil {
		return nil, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	// Wrap the discovery client in a memory cache
	return memory.NewMemCacheClient(discoveryClient), nil
}

func (c *clientConfigRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	// Get the cached discovery client
	discoveryClient, err := c.ToDiscoveryClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	// Retrieve the API group resources
	apiGroupResources, err := restmapper.GetAPIGroupResources(discoveryClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get API group resources: %w", err)
	}

	// Create and return a RESTMapper
	return restmapper.NewDiscoveryRESTMapper(apiGroupResources), nil
}

// ToRawKubeConfigLoader provides access to the raw kubeconfig loader
func (c *clientConfigRESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return c.config
}
