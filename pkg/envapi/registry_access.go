/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/rs/zerolog/log"
)

// RegistryAccess is what a registry answered about access to an image.
type RegistryAccess int

const (
	RegistryAccessUnknown      RegistryAccess = iota // Access is allowed, or the registry could not be asked
	RegistryAccessDenied                             // The credentials were rejected (HTTP 401 or 403)
	RegistryRepositoryNotFound                       // The repository does not exist, or the credentials cannot see it
	RegistryImageNotFound                            // The repository exists but the tag does not (pull only)
)

// RegistryCredentialsSuggestion is the suggestion for a registry that rejected the environment's
// credentials.
const RegistryCredentialsSuggestion = "Check the environment's registry credentials"

// registryAccessTimeout bounds CheckRegistryAccess, which runs only to explain a failure.
const registryAccessTimeout = 15 * time.Second

// CheckRegistryAccess asks the registry, with creds, whether they can pull imageRef, or push it when
// push is true. Pull and push failures reported by the Docker daemon often carry no status code,
// and their messages depend on the registry and the daemon's image store. The registry's own HTTP
// status tells credential problems apart from missing repositories and tags.
//
// A push check initiates a blob upload, as go-containerregistry's CheckPushPermission does, and
// cancels it. Call it only to explain a failure.
func CheckRegistryAccess(ctx context.Context, creds *DockerCredentials, imageRef string, push bool) RegistryAccess {
	ref, err := name.ParseReference(imageRef, name.WithDefaultRegistry(creds.RegistryURL))
	if err != nil {
		return RegistryAccessUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, registryAccessTimeout)
	defer cancel()

	authenticator := registryAuthenticator(creds)
	if push {
		// CheckPushPermission takes no context, so its requests get ctx through the transport.
		err = remote.CheckPushPermission(ref, staticKeychain{authenticator}, contextTransport{ctx, remote.DefaultTransport})
	} else {
		// GET rather than HEAD: only a response body says whether the repository or the tag is missing.
		_, err = remote.Get(ref, remote.WithAuth(authenticator), remote.WithContext(ctx), remote.WithRetryBackoff(remote.Backoff{Steps: 1}))
	}
	access := registryAccessFromError(err, push)
	log.Debug().Err(err).Msgf("Registry access check for %s (push=%v): %d", imageRef, push, access)
	return access
}

// registryAccessFromError classifies an error from go-containerregistry by the registry's answer.
// Any other error, such as a network failure, says nothing about access.
func registryAccessFromError(err error, push bool) RegistryAccess {
	transportErr, ok := errors.AsType[*transport.Error](err)
	if !ok {
		return RegistryAccessUnknown
	}
	switch transportErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return RegistryAccessDenied
	}
	for _, diagnostic := range transportErr.Errors {
		switch diagnostic.Code {
		case transport.UnauthorizedErrorCode, transport.DeniedErrorCode:
			return RegistryAccessDenied
		case transport.NameUnknownErrorCode:
			return RegistryRepositoryNotFound
		case transport.ManifestUnknownErrorCode:
			return RegistryImageNotFound
		}
	}
	if transportErr.StatusCode == http.StatusNotFound {
		if push {
			return RegistryRepositoryNotFound // A push check only touches the repository
		}
		return RegistryImageNotFound
	}
	return RegistryAccessUnknown
}

// contextTransport sends every request with ctx, which bounds and cancels them all.
type contextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t contextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(req.WithContext(t.ctx))
}

// staticKeychain resolves every registry to one authenticator, for CheckPushPermission.
type staticKeychain struct{ authenticator authn.Authenticator }

func (k staticKeychain) Resolve(authn.Resource) (authn.Authenticator, error) {
	return k.authenticator, nil
}
