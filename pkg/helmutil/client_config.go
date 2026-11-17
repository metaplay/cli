/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"fmt"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/kube"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// The field manager name that Kubernetes records for changes made through Helm.
const helmFieldManager = "metaplay-cli"

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

	// Name the field manager that Kubernetes records for Helm's changes. Helm
	// defaults to the binary name, which differs between platforms ('metaplay',
	// 'metaplay.exe') and with 'go run'. Under server-side apply, a field removed
	// from the chart is only deleted if the same manager applied it before, so
	// the name must be the same everywhere.
	kube.ManagedFieldsManager = helmFieldManager

	// Initialize Helm action configuration. The logger must be set before Init,
	// which copies it into the Kubernetes client and the storage driver.
	actionConfig := action.NewConfiguration(action.ConfigurationSetLogger(NewDebugLogHandler()))
	if err := actionConfig.Init(restGetter, namespace, "secret"); err != nil {
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
