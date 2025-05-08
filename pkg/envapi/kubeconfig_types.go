/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"k8s.io/client-go/pkg/apis/clientauthentication"
)

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
