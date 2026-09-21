/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/metahttp"
)

// Where a stack's image repository is, and how the CLI finds out.
//
// A stack either serves its own registry or it does not, and the CLI has no
// other way to tell: it asks, and a 404 means this stack still keeps its images
// in a cloud registry the old path reaches. That makes the difference between
// "not served" and "failed" load-bearing — treating a failure as "not served"
// would send the caller down the fallback and report whatever that fails with,
// which has nothing to do with what went wrong.

func testEnvironment(t *testing.T, handler http.Handler) *TargetEnvironment {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	tokenSet := &auth.TokenSet{}
	client := metahttp.NewJSONClient(tokenSet, server.URL)

	// The shared client retries 5xx with a backoff, which is right against a
	// real stack and pure waiting here: what these tests pin is how a status
	// maps to an error, and retrying it three times first says nothing extra
	// while adding ten seconds to the package's test run.
	client.Resty.SetRetryCount(0)

	return &TargetEnvironment{
		TokenSet:        tokenSet,
		StackApiBaseURL: server.URL,
		HumanID:         "lovely-wombats-build-nimbly",
		StackApiClient:  client,
	}
}

func serveRegistryCredentials(t *testing.T, credentials RegistryCredentials) (*TargetEnvironment, *string) {
	t.Helper()
	var requestedPath string
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(credentials)
	}))
	return env, &requestedPath
}

func TestGetRegistryCredentials_ReportsWhereImagesGoAndHowToAuthenticate(t *testing.T) {
	env, path := serveRegistryCredentials(t, RegistryCredentials{
		RegistryHost: "registry.example-stack.example.com",
		Repository:   "lovely-wombats-build-nimbly/gameserver",
		Username:     "developer",
		Password:     "signed-assertion",
	})

	credentials, err := env.GetRegistryCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if *path != "/v0/credentials/lovely-wombats-build-nimbly/registry" {
		t.Errorf("asked %s, want the environment's own credentials path", *path)
	}
	if credentials.RegistryHost != "registry.example-stack.example.com" {
		t.Errorf("registry host = %q", credentials.RegistryHost)
	}
	if credentials.Repository != "lovely-wombats-build-nimbly/gameserver" {
		t.Errorf("repository = %q", credentials.Repository)
	}
	if credentials.Username != "developer" || credentials.Password != "signed-assertion" {
		t.Errorf("credential = %q/%q", credentials.Username, credentials.Password)
	}
}

// The push target joins the two halves the stack deliberately keeps apart. The
// stack carries host and repository separately so one shape holds registries
// that are laid out differently; a client that pushes has to join them, and
// this is the only place that does.
func TestGetRegistryCredentials_PushTargetIsHostQualified(t *testing.T) {
	env, _ := serveRegistryCredentials(t, RegistryCredentials{
		RegistryHost: "registry.example-stack.example.com",
		Repository:   "lovely-wombats-build-nimbly/gameserver",
		Username:     "developer",
		Password:     "signed-assertion",
	})

	credentials, err := env.GetRegistryCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "registry.example-stack.example.com/lovely-wombats-build-nimbly/gameserver"
	if got := credentials.PushTarget(); got != want {
		t.Errorf("push target = %q, want %q", got, want)
	}
}

// A stack that does not serve this endpoint keeps its images somewhere the
// older path reaches. That is not an error, and the caller has to be able to
// tell it apart from one.
func TestGetRegistryCredentials_NotServedIsNotAFailure(t *testing.T) {
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))

	_, err := env.GetRegistryCredentials()

	if !errors.Is(err, ErrRegistryCredentialsNotServed) {
		t.Fatalf("error = %v, want it to report that the stack does not serve this", err)
	}
}

// Everything else is a failure and must stay one. A stack whose registry
// endpoint is broken would otherwise be reported as whatever the fallback
// happens to fail with, which names neither the endpoint nor the stack.
func TestGetRegistryCredentials_OtherFailuresAreNotMistakenForNotServed(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusInternalServerError,
		http.StatusBadGateway,
	} {
		env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", status)
		}))

		_, err := env.GetRegistryCredentials()

		if err == nil {
			t.Errorf("status %d: expected an error", status)
			continue
		}
		if errors.Is(err, ErrRegistryCredentialsNotServed) {
			t.Errorf("status %d: reported as 'not served', which would silently take the fallback", status)
		}
	}
}

// A stack answering 200 with nothing useful is a stack that cannot be pushed
// to. Saying so here names the endpoint; letting it through produces a docker
// error about an empty reference somewhere much further down.
func TestGetRegistryCredentials_AnIncompleteAnswerIsRefused(t *testing.T) {
	incomplete := map[string]struct {
		credentials RegistryCredentials
		names       string
	}{
		"no registry host": {RegistryCredentials{Repository: "env/gameserver", Username: "u", Password: "p"}, "registry host"},
		"no repository":    {RegistryCredentials{RegistryHost: "registry.example.com", Username: "u", Password: "p"}, "repository"},
		"no username":      {RegistryCredentials{RegistryHost: "registry.example.com", Repository: "env/gameserver", Password: "p"}, "username"},
		"no password":      {RegistryCredentials{RegistryHost: "registry.example.com", Repository: "env/gameserver", Username: "u"}, "password"},
		// Several missing at once. The message must name the same one every
		// run: an error that varies is one nobody can search for, and finding
		// it by ranging a map is how that happens.
		"nothing at all": {RegistryCredentials{}, "registry host"},
	}
	for name, tc := range incomplete {
		t.Run(name, func(t *testing.T) {
			env, _ := serveRegistryCredentials(t, tc.credentials)

			_, err := env.GetRegistryCredentials()
			if err == nil {
				t.Fatal("expected an error naming what the stack left out")
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("error = %q, want it to name the missing %q", err, tc.names)
			}
		})
	}
}

// An answer whose fields are all present but do not join into a name any
// registry client parses is refused the same way an incomplete one is. A
// scheme on the host is how that happens in practice, and it is invisible
// until docker rejects the reference several steps later.
func TestGetRegistryCredentials_AnUnparseableRepositoryIsRefused(t *testing.T) {
	env, _ := serveRegistryCredentials(t, RegistryCredentials{
		RegistryHost: "https://registry.example-stack.example.com",
		Repository:   "lovely-wombats-build-nimbly/gameserver",
		Username:     "developer",
		Password:     "signed-assertion",
	})

	_, err := env.GetRegistryCredentials()
	if err == nil {
		t.Fatal("expected an error naming the field that cannot be pushed to")
	}
	if !strings.Contains(err.Error(), "https://registry.example-stack.example.com") {
		t.Errorf("error = %q, want it to name the host it could not parse", err)
	}
	if !strings.Contains(err.Error(), "registry host") {
		t.Errorf("error = %q, want it to name which field was unusable", err)
	}
}

// Where the stack issues a credential, that is the whole answer: nothing else
// is consulted, and in particular nothing cloud-shaped is fetched.
//
// The second half is the point. Asking for the environment's cloud description
// first and deciding after is the shape of the problem this replaces — a push
// that failed before it reached any registry, because that description only
// exists for a cloud-provisioned environment.
func TestResolveImagePushTarget_UsesWhatTheStackIssued(t *testing.T) {
	var asked []string
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RegistryCredentials{
			RegistryHost: "registry.example-stack.example.com",
			Repository:   "lovely-wombats-build-nimbly/gameserver",
			Username:     "developer",
			Password:     "signed-assertion",
		})
	}))

	pushTarget, err := env.ResolveImagePushTarget()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "registry.example-stack.example.com/lovely-wombats-build-nimbly/gameserver"
	if pushTarget.Repository != want {
		t.Errorf("repository = %q, want %q", pushTarget.Repository, want)
	}
	if pushTarget.Credentials.Username != "developer" {
		t.Errorf("username = %q", pushTarget.Credentials.Username)
	}
	if pushTarget.Credentials.RegistryURL != "registry.example-stack.example.com" {
		t.Errorf("registry = %q, want the host the credential names", pushTarget.Credentials.RegistryURL)
	}
	for _, path := range asked {
		if strings.Contains(path, "/deployments/") {
			t.Errorf("fetched %s, which only a cloud-provisioned environment has", path)
		}
	}
}

// A stack that issues no credential and whose environment names no repository
// either cannot be pushed to at all. Saying that names both halves of why;
// reaching for cloud credentials anyway would report whatever that failed with,
// which names neither.
func TestResolveImagePushTarget_NoCredentialAndNoRepositoryIsRefusedClearly(t *testing.T) {
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/registry") {
			http.NotFound(w, r)
			return
		}
		// An environment naming no repository, which is what a stack that has
		// not provisioned one looks like.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeploymentSecret{})
	}))

	_, err := env.ResolveImagePushTarget()

	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "image repository") {
		t.Errorf("error = %q, want it to name the missing repository", err)
	}
}

// A 404 has two meanings and they must not be confused. An environment that is
// not on this stack gets one from a check that runs before the registry
// endpoint is reached, so it arrives looking exactly like a stack that has no
// such endpoint. Following the fallback and reporting what *that* failed with
// would send the reader after an environment description that was never the
// problem.
func TestResolveImagePushTarget_AnUnknownEnvironmentSaysSo(t *testing.T) {
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Both requests answer 404, which is what an unknown environment gets
		// whether or not the stack serves its own registry.
		http.NotFound(w, r)
	}))

	_, err := env.ResolveImagePushTarget()

	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "lovely-wombats-build-nimbly") {
		t.Errorf("error = %q, want it to name the environment", err)
	}
	if strings.Contains(err.Error(), "/deployments/") {
		t.Errorf("error = %q, want it to name the environment rather than the request that failed", err)
	}
}

// The fallback's other leg: a stack that issues no credential but whose
// environment does name a repository proceeds to the older path rather than
// refusing. Only the branch is asserted here — where it leads needs a cloud
// registry, which no test should reach — but the branch is the part that can
// be got wrong, and getting it wrong turns every push on an older stack into a
// refusal.
func TestResolveImagePushTarget_AnEnvironmentWithARepositoryTakesTheOlderPath(t *testing.T) {
	var asked []string
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if strings.Contains(r.URL.Path, "/registry") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		details := DeploymentSecret{}
		details.Deployment.EcrRepo = "123456789.dkr.ecr.eu-west-1.amazonaws.com/an-environment"
		_ = json.NewEncoder(w).Encode(details)
	}))

	_, err := env.ResolveImagePushTarget()

	// It gets as far as asking the stack for cloud credentials, which is where
	// a test without a cloud account stops — the stack here answers that with
	// an environment description, which is not a credential, so the older path
	// ends in an error rather than reaching any cloud service. Asserting the
	// request was made is what pins the branch: a refusal or a silent success
	// would both leave it unasked.
	if !slices.ContainsFunc(asked, func(path string) bool { return strings.HasSuffix(path, "/aws") }) {
		t.Errorf("requested %v, want the older path's cloud credentials request", asked)
	}

	// What must NOT happen is the refusal that belongs to an environment naming
	// no repository at all.
	if err != nil && strings.Contains(err.Error(), "no image repository") {
		t.Errorf("error = %q, want it to have taken the older path rather than refusing", err)
	}
}
