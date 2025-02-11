/*
 * Copyright Metaplay. All rights reserved.
 */
package envapi

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Kubernetes client that wraps all the various Kubernetes client configs
// and client types into one struct for convenient use.
type KubeClient struct {
	Namespace     string
	KubeConfig    string
	RestConfig    *rest.Config
	RestClient    *rest.RESTClient
	Clientset     *kubernetes.Clientset
	DynamicClient *dynamic.DynamicClient
}
