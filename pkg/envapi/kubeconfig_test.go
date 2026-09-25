/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/metaplay/cli/pkg/auth"
)

const (
	kubeconfigEnvironment = "tiny-squids"
	kubeconfigUser        = "developer@example.com"
	clusterServer         = "https://kubernetes.example.test:6443"
	clusterAuthority      = "-----BEGIN CERTIFICATE-----\nnot-a-real-ca\n-----END CERTIFICATE-----\n"
)

// fakeStackAPI answers the credentials endpoint the way a stack does: with a
// kubeconfig when asked for one, and with an exec credential when asked for
// that. It remembers what it was asked.
type fakeStackAPI struct {
	server           *httptest.Server
	kubeconfig       string // the kubeconfig a request asking for no particular type gets
	clusterAuthority string // the certificate authority an exec credential names

	mu       sync.Mutex
	requests []string
}

func newFakeStackAPI(t *testing.T) *fakeStackAPI {
	t.Helper()
	fake := &fakeStackAPI{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requests = append(fake.requests, r.Method+" "+r.URL.RequestURI())
		kubeconfig, authority := fake.kubeconfig, fake.clusterAuthority
		fake.mu.Unlock()

		if r.URL.Path != "/stackapi/v0/credentials/"+kubeconfigEnvironment+"/k8s" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		switch r.URL.Query().Get("type") {
		case "", "kubeconfig":
			_, _ = fmt.Fprint(w, kubeconfig)
		case "execcredential":
			// What every stack answers an exec credential with: a
			// ServiceAccount token, and the cluster's own endpoint.
			_, _ = fmt.Fprintf(w, `{"apiVersion":"client.authentication.k8s.io/v1beta1","kind":"ExecCredential",`+
				`"spec":{"cluster":{"server":%q,"certificateAuthorityData":%q}},`+
				`"status":{"expirationTimestamp":"2026-09-24T13:00:00Z","token":"a-service-account-token"}}`,
				clusterServer, base64.StdEncoding.EncodeToString([]byte(authority)))
		default:
			http.Error(w, "invalid type", http.StatusBadRequest)
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeStackAPI) baseURL() string {
	return f.server.URL + "/stackapi"
}

func (f *fakeStackAPI) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeStackAPI) target() *TargetEnvironment {
	return f.targetAt(f.baseURL())
}

// targetAt is the environment as the CLI reaches it at stackAPIBaseURL, which
// may spell the fake's address otherwise.
func (f *fakeStackAPI) targetAt(stackAPIBaseURL string) *TargetEnvironment {
	return NewTargetEnvironmentAtStackAPI(&auth.TokenSet{AccessToken: "the-callers-access-token"}, stackAPIBaseURL, kubeconfigEnvironment)
}

// servedKubeconfig is a kubeconfig as a stack writes one: one cluster, one user
// carrying token, one context in the environment's namespace.
func servedKubeconfig(server, authority, token string) string {
	authorityLine := ""
	if authority != "" {
		authorityLine = "    certificate-authority-data: " + base64.StdEncoding.EncodeToString([]byte(authority)) + "\n"
	}
	return "apiVersion: v1\nclusters:\n- cluster:\n" + authorityLine +
		"    server: " + server + "\n  name: " + kubeconfigEnvironment + "\n" +
		"contexts:\n- context:\n    cluster: " + kubeconfigEnvironment + "\n    namespace: " + kubeconfigEnvironment +
		"\n    user: user\n  name: " + kubeconfigEnvironment + "\n" +
		"current-context: " + kubeconfigEnvironment + "\nkind: Config\n" +
		"users:\n- name: user\n  user:\n    token: " + token + "\n"
}

// dynamicKubeconfig is what the CLI emits: the server and authority it was
// given, and a user whose credential comes from running the CLI with pluginArgs.
func dynamicKubeconfig(server, authority string, pluginArgs ...string) string {
	authorityLine := "        certificate-authority-data: \"\"\n"
	if authority != "" {
		authorityLine = "        certificate-authority-data: " + base64.StdEncoding.EncodeToString([]byte(authority)) + "\n"
	}
	args := ""
	for _, arg := range pluginArgs {
		args += "                - " + arg + "\n"
	}
	return "apiVersion: v1\nclusters:\n    - cluster:\n" + authorityLine +
		"        server: " + server + "\n      name: " + server + "\n" +
		"contexts:\n    - context:\n        cluster: " + server + "\n        user: " + kubeconfigUser +
		"\n        namespace: " + kubeconfigEnvironment + "\n      name: " + kubeconfigEnvironment + "\n" +
		"current-context: " + kubeconfigEnvironment + "\nkind: Config\npreferences: {}\n" +
		"users:\n    - name: " + kubeconfigUser + "\n      user:\n        exec:\n            command: metaplay\n" +
		"            args:\n" + args +
		"            apiVersion: client.authentication.k8s.io/v1beta1\n            interactiveMode: Never\n"
}

// A stack that serves no proxy answers a kubeconfig request with the cluster's
// own, and gets the dynamic kubeconfig it always has: the cluster's endpoint
// and authority, and a plugin fetching a Kubernetes credential from StackAPI.
// Pinned as the bytes an older CLI wrote, so that nobody with such a stack
// sees their kubeconfig change.
func TestGetKubeConfigWithExecCredential_IsUnchangedWhereTheStackServesNoProxy(t *testing.T) {
	tests := []struct {
		name      string
		authority string
	}{
		{"with a cluster authority", clusterAuthority},
		{"with no cluster authority", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stack := newFakeStackAPI(t)
			stack.kubeconfig = servedKubeconfig(clusterServer, test.authority, "a-service-account-token")
			stack.clusterAuthority = test.authority

			kubeconfig, err := stack.target().GetKubeConfigWithExecCredential(kubeconfigUser, true)
			if err != nil {
				t.Fatalf("GetKubeConfigWithExecCredential: %v", err)
			}

			want := dynamicKubeconfig(clusterServer, test.authority,
				"get", "kubernetes-execcredential", kubeconfigEnvironment, stack.baseURL())
			if kubeconfig != want {
				t.Errorf("emitted kubeconfig:\n%s\nwant:\n%s", kubeconfig, want)
			}
			if asked := stack.asked(); len(asked) != 1 {
				t.Errorf("asked StackAPI %d times, want once: %v", len(asked), asked)
			}
		})
	}
}

// A stack serving the Kubernetes API proxy answers a kubeconfig request with
// one naming StackAPI, and that is how the CLI tells: no capability to ask
// for, and no second request. The dynamic kubeconfig then names the same
// server, and its plugin answers with the CLI's own access token, which the
// proxy takes.
func TestGetKubeConfigWithExecCredential_PointsAtTheProxyWhereTheStackServesOne(t *testing.T) {
	stack := newFakeStackAPI(t)
	proxyServer := stack.baseURL() + "/tenant/v1/" + kubeconfigEnvironment + "/k8s"
	stack.kubeconfig = servedKubeconfig(proxyServer, "", "a-proxy-credential")

	kubeconfig, err := stack.target().GetKubeConfigWithExecCredential(kubeconfigUser, true)
	if err != nil {
		t.Fatalf("GetKubeConfigWithExecCredential: %v", err)
	}

	want := dynamicKubeconfig(proxyServer, "", "get", "kubernetes-execcredential", kubeconfigEnvironment, "--proxy")
	if kubeconfig != want {
		t.Errorf("emitted kubeconfig:\n%s\nwant:\n%s", kubeconfig, want)
	}
	// The credential in the kubeconfig StackAPI answered with is for a
	// static kubeconfig, and a dynamic one carries none.
	if strings.Contains(kubeconfig, "a-proxy-credential") {
		t.Errorf("the dynamic kubeconfig carries the credential the stack's kubeconfig did:\n%s", kubeconfig)
	}
	if asked := stack.asked(); len(asked) != 1 {
		t.Errorf("asked StackAPI %d times, want once: %v", len(asked), asked)
	}
}

// A stack whose StackAPI certificate is not publicly trusted names its own
// authority, and the dynamic kubeconfig carries it on.
func TestGetKubeConfigWithExecCredential_CarriesTheProxysCertificateAuthority(t *testing.T) {
	stack := newFakeStackAPI(t)
	proxyServer := stack.baseURL() + "/tenant/v1/" + kubeconfigEnvironment + "/k8s"
	const stacksOwn = "-----BEGIN CERTIFICATE-----\nthe-stacks-own-authority\n-----END CERTIFICATE-----\n"
	stack.kubeconfig = servedKubeconfig(proxyServer, stacksOwn, "a-proxy-credential")

	kubeconfig, err := stack.target().GetKubeConfigWithExecCredential(kubeconfigUser, true)
	if err != nil {
		t.Fatalf("GetKubeConfigWithExecCredential: %v", err)
	}

	want := dynamicKubeconfig(proxyServer, stacksOwn, "get", "kubernetes-execcredential", kubeconfigEnvironment, "--proxy")
	if kubeconfig != want {
		t.Errorf("emitted kubeconfig:\n%s\nwant:\n%s", kubeconfig, want)
	}
}

// A kubeconfig the CLI cannot find a server in is not one it can write a
// dynamic kubeconfig from.
func TestGetKubeConfigWithExecCredential_RefusesAKubeconfigNamingNoServer(t *testing.T) {
	tests := []struct {
		name   string
		served string
	}{
		{"not a kubeconfig", "{not yaml"},
		{"no current context", "apiVersion: v1\nkind: Config\nclusters: []\n"},
		{"no server", strings.Replace(servedKubeconfig(clusterServer, "", "t"), "    server: "+clusterServer+"\n", "", 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stack := newFakeStackAPI(t)
			stack.kubeconfig = test.served

			if _, err := stack.target().GetKubeConfigWithExecCredential(kubeconfigUser, true); err == nil {
				t.Error("wrote a dynamic kubeconfig from one naming no server")
			}
		})
	}
}

// A stack naming StackAPI somewhere other than where the CLI reached it is
// refused, rather than taken for a cluster and written a plugin the proxy will
// not accept, or trusted with the CLI's access token.
func TestGetKubeConfigWithExecCredential_RefusesStackAPIElsewhere(t *testing.T) {
	stack := newFakeStackAPI(t)
	tests := []struct {
		name   string
		origin string
	}{
		{"on another host", "https://infra.elsewhere.example.com"},
		{"over another scheme", strings.Replace(stack.server.URL, "http://", "https://", 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stack.kubeconfig = servedKubeconfig(test.origin+"/stackapi/tenant/v1/"+kubeconfigEnvironment+"/k8s", "", "a-proxy-credential")

			_, err := stack.target().GetKubeConfigWithExecCredential(kubeconfigUser, true)
			if err == nil {
				t.Fatal("wrote a dynamic kubeconfig for StackAPI elsewhere")
			}
			if !strings.Contains(err.Error(), test.origin) {
				t.Errorf("error = %q, want it to name %s", err, test.origin)
			}
			if !errors.Is(err, ErrKubernetesAPIProxyRefused) {
				t.Errorf("error = %q, want it to wrap ErrKubernetesAPIProxyRefused", err)
			}
		})
	}
}

// The proxy plugin answers with the default auth provider's token, so an
// environment signing in with another provider is refused rather than have
// that token handed to its stack.
func TestGetKubeConfigWithExecCredential_RefusesTheProxyForAnotherAuthProvider(t *testing.T) {
	stack := newFakeStackAPI(t)
	stack.kubeconfig = servedKubeconfig(stack.baseURL()+"/tenant/v1/"+kubeconfigEnvironment+"/k8s", "", "a-proxy-credential")

	_, err := stack.target().GetKubeConfigWithExecCredential(kubeconfigUser, false)
	if !errors.Is(err, ErrKubernetesAPIProxyRefused) {
		t.Fatalf("error = %v, want it to wrap ErrKubernetesAPIProxyRefused", err)
	}
}

// A stack serving no proxy is unaffected by the auth provider, as before.
func TestGetKubeConfigWithExecCredential_TakesTheClusterForAnotherAuthProvider(t *testing.T) {
	stack := newFakeStackAPI(t)
	stack.kubeconfig = servedKubeconfig(clusterServer, "", "a-service-account-token")

	kubeconfig, err := stack.target().GetKubeConfigWithExecCredential(kubeconfigUser, false)
	if err != nil {
		t.Fatalf("GetKubeConfigWithExecCredential: %v", err)
	}
	want := dynamicKubeconfig(clusterServer, "", "get", "kubernetes-execcredential", kubeconfigEnvironment, stack.baseURL())
	if kubeconfig != want {
		t.Errorf("emitted kubeconfig:\n%s\nwant:\n%s", kubeconfig, want)
	}
}

func TestIsKubernetesAPIProxy_NormalizesPortAndTrailingSlash(t *testing.T) {
	tests := []struct {
		name       string
		stackAPI   string
		server     string
		wantProxy  bool
		wantRefuse bool
	}{
		{"explicit default https port", "https://infra.example.com/stackapi", "https://infra.example.com:443/stackapi/tenant/v1/e/k8s", true, false},
		{"base URL with explicit default port", "https://infra.example.com:443/stackapi", "https://infra.example.com/stackapi/tenant/v1/e/k8s", true, false},
		{"base URL with a trailing slash", "https://infra.example.com/stackapi/", "https://infra.example.com/stackapi/tenant/v1/e/k8s", true, false},
		{"another port", "https://infra.example.com/stackapi", "https://infra.example.com:8443/stackapi/tenant/v1/e/k8s", false, true},
		{"a cluster", "https://infra.example.com/stackapi", clusterServer, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := &TargetEnvironment{StackApiBaseURL: test.stackAPI}
			isProxy, err := target.isKubernetesAPIProxy(test.server)
			if test.wantRefuse != (err != nil) {
				t.Fatalf("err = %v, want refused %v", err, test.wantRefuse)
			}
			if isProxy != test.wantProxy {
				t.Errorf("isProxy = %v, want %v", isProxy, test.wantProxy)
			}
		})
	}
}

// Host names are case-insensitive, so one spelled otherwise is the same host.
func TestGetKubeConfigWithExecCredential_TakesTheProxyOnTheSameHostSpelledOtherwise(t *testing.T) {
	stack := newFakeStackAPI(t)
	reachedAt := strings.Replace(stack.baseURL(), "127.0.0.1", "localhost", 1)
	proxyServer := strings.Replace(reachedAt, "localhost", "LocalHost", 1) + "/tenant/v1/" + kubeconfigEnvironment + "/k8s"
	stack.kubeconfig = servedKubeconfig(proxyServer, "", "a-proxy-credential")

	kubeconfig, err := stack.targetAt(reachedAt).GetKubeConfigWithExecCredential(kubeconfigUser, true)
	if err != nil {
		t.Fatalf("GetKubeConfigWithExecCredential: %v", err)
	}

	want := dynamicKubeconfig(proxyServer, "", "get", "kubernetes-execcredential", kubeconfigEnvironment, "--proxy")
	if kubeconfig != want {
		t.Errorf("emitted kubeconfig:\n%s\nwant:\n%s", kubeconfig, want)
	}
}
