/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/rs/zerolog/log"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/metahttp"
)

// ErrRegistryCredentialsNotServed reports that this stack issues no registry
// credentials of its own, so the caller should reach the environment's images
// the way it did before.
//
// A stack says so by not serving the endpoint at all: answering 200 with an
// empty body would be indistinguishable from a stack that is simply broken.
var ErrRegistryCredentialsNotServed = errors.New("this stack does not issue image registry credentials")

// RegistryCredentials is where an environment's images live, and what to
// present to push or pull them.
//
// Host and repository stay separate because that is how the stack holds them,
// and one shape then covers registries laid out differently. PushTarget is the
// only place the two are joined.
type RegistryCredentials struct {
	RegistryHost string `json:"registry_host"`
	Repository   string `json:"repository"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

// PushTarget is the host-qualified repository an image is pushed to.
func (c *RegistryCredentials) PushTarget() string {
	return c.RegistryHost + "/" + c.Repository
}

// DockerCredentials is what a docker client presents for this registry.
func (c *RegistryCredentials) DockerCredentials() *DockerCredentials {
	return &DockerCredentials{
		Username:    c.Username,
		Password:    c.Password,
		RegistryURL: c.RegistryHost,
	}
}

// GetRegistryCredentials asks the stack for a credential to this environment's
// image repository.
//
// The credential is short-lived and nothing stores it — not here, and not in
// the docker credential store, which this CLI never writes to. It is handed to
// the docker daemon inline for the one push that needs it.
//
// The stack decides which repository the credential reaches, so nothing here
// sends one.
func (target *TargetEnvironment) GetRegistryCredentials() (*RegistryCredentials, error) {
	path := fmt.Sprintf("/v0/credentials/%s/registry", target.HumanID)
	log.Debug().Msgf("Get image registry credentials from %s%s", target.StackApiClient.BaseURL, path)

	// A 404 is expected here: it is how a stack says it keeps its images
	// elsewhere. Every push against such a stack takes this path and recovers
	// from it, so it must not be logged as a failed request.
	credentials, err := metahttp.PostExpecting[RegistryCredentials](target.StackApiClient, path, nil, "", http.StatusNotFound)
	if err != nil {
		if isHTTPNotFound(err) {
			return nil, ErrRegistryCredentialsNotServed
		}
		return nil, clierrors.Wrap(err, "Failed to get the environment's image registry credentials").
			WithSuggestion("Check that you have access to this environment, and that its stack is reachable")
	}

	// Checked here rather than where the push fails: a missing field surfaces
	// much further down as a docker error about a malformed reference or a
	// rejected login, naming neither the stack nor this endpoint.
	for _, missing := range []struct{ field, value string }{
		{"registry host", credentials.RegistryHost},
		{"repository", credentials.Repository},
		{"username", credentials.Username},
		{"password", credentials.Password},
	} {
		if missing.value == "" {
			return nil, clierrors.Newf("The environment's registry credential names no %s", missing.field).
				WithDetails("The stack answered successfully but left the field out, so there is nothing to push to.").
				WithSuggestion("Report this to whoever operates the stack; nothing can be done from here")
		}
	}

	// The two halves are joined here and nowhere else, so this is where a host
	// or repository no registry client will parse has to be caught. A scheme on
	// the host is how that happens in practice.
	if _, err := name.NewRegistry(credentials.RegistryHost, name.StrictValidation); err != nil {
		return nil, unusableRegistryCredential(err, "registry host", credentials.RegistryHost)
	}
	if _, err := name.NewRepository(credentials.PushTarget(), name.StrictValidation); err != nil {
		return nil, unusableRegistryCredential(err, "repository", credentials.PushTarget())
	}

	return &credentials, nil
}

// unusableRegistryCredential reports a field the stack filled in with something
// no registry client accepts, naming the field and what it held.
func unusableRegistryCredential(cause error, field, value string) error {
	return clierrors.Wrapf(cause, "The environment's registry credential names a %s that cannot be pushed to: '%s'", field, value).
		WithDetails("The stack answered successfully, but what it answered is not a name a docker registry accepts.").
		WithSuggestion("Report this to whoever operates the stack; nothing can be done from here")
}

// isHTTPNotFound reports whether err carries a 404 from the StackAPI.
func isHTTPNotFound(err error) bool {
	httpErr, ok := errors.AsType[*metahttp.HTTPError](err)
	return ok && httpErr.StatusCode == http.StatusNotFound
}

// ImagePushTarget is where an image goes and what authenticates the push.
type ImagePushTarget struct {
	// Repository is host-qualified, ready to be tagged and pushed to.
	Repository  string
	Credentials *DockerCredentials
}

// ResolveImagePushTarget answers where this environment's images go: whatever
// the stack issues a credential for, and for a stack that issues none, the
// cloud registry the older path reaches.
//
// Nothing cloud-shaped is fetched before that fallback is taken. Asking first
// and deciding after is what made every push depend on a description only a
// cloud-provisioned environment has.
//
// The 404 that selects the fallback is also what an unknown environment gets,
// from a check that runs before the endpoint is reached. The two are told
// apart by what the second request finds — an environment on an older stack
// has a description, one that does not exist has neither — rather than by
// guessing at the first 404.
func (target *TargetEnvironment) ResolveImagePushTarget() (*ImagePushTarget, error) {
	credentials, err := target.GetRegistryCredentials()
	switch {
	case err == nil:
		return &ImagePushTarget{
			Repository:  credentials.PushTarget(),
			Credentials: credentials.DockerCredentials(),
		}, nil
	case errors.Is(err, ErrRegistryCredentialsNotServed):
		log.Debug().Msg("Stack issues no registry credentials; using the environment's cloud registry")
	default:
		return nil, err
	}

	envDetails, err := target.GetDetails()
	if err != nil {
		if isHTTPNotFound(err) {
			// Neither request found anything: the environment is not on this
			// stack at all. Reporting the deployments request would point the
			// reader at a description that was never the problem.
			return nil, clierrors.Newf("Environment '%s' was not found on this stack", target.HumanID).
				WithSuggestion("Check the environment name, and run 'metaplay update project-environments' to sync the list from the portal")
		}
		return nil, clierrors.Wrap(err, "Failed to read the environment's details").
			WithSuggestion("Check that you have access to this environment, and that its stack is reachable")
	}
	if envDetails.Deployment.EcrRepo == "" {
		return nil, clierrors.New("The environment has no image repository").
			WithDetails("Its stack issues no registry credentials, and the environment names no repository of its own.").
			WithSuggestion("Check that the environment finished provisioning, and that your CLI is up to date")
	}

	dockerCredentials, err := target.GetDockerCredentials(envDetails)
	if err != nil {
		return nil, clierrors.Wrap(err, "Failed to get credentials for the environment's image repository").
			WithSuggestion("Check that you have access to this environment")
	}
	return &ImagePushTarget{
		Repository:  envDetails.Deployment.EcrRepo,
		Credentials: dockerCredentials,
	}, nil
}
