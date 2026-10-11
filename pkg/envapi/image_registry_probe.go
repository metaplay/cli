/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	clierrors "github.com/metaplay/cli/internal/errors"
)

// PushPath is which path a push or deploy takes to an environment's images.
type PushPath string

const (
	// PushPathCredentialsEndpoint is a credential the stack issues, for
	// whichever registry it keeps the environment's images in.
	PushPathCredentialsEndpoint PushPath = "credentials-endpoint"
	// PushPathOlder is the repository the environment's description names,
	// in ECR, reached with AWS credentials the stack issues: how a stack older
	// than the registry credentials endpoint has its images reached.
	PushPathOlder PushPath = "older-path"
	// PushPathNone is a push that fails before it reaches any registry.
	PushPathNone PushPath = "none"
)

// CredentialsEndpointStatus is what the registry credentials endpoint
// answered.
type CredentialsEndpointStatus string

const (
	CredentialsEndpointServed    CredentialsEndpointStatus = "served"
	CredentialsEndpointNotServed CredentialsEndpointStatus = "not-served"
	CredentialsEndpointFailed    CredentialsEndpointStatus = "failed"
)

// OlderPathStatus is what the older path to an environment's images found.
type OlderPathStatus string

const (
	OlderPathReachable OlderPathStatus = "reachable"
	// OlderPathAbsent is an environment the older path has nothing to reach
	// for: its stack does not describe it, or its description names no
	// repository. Not a failure — it is what an environment whose images are
	// not in ECR looks like.
	OlderPathAbsent OlderPathStatus = "absent"
	OlderPathFailed OlderPathStatus = "failed"
)

// ImageRegistryProbe is what each path to an environment's images answered,
// and which of them a push or deploy would take.
//
// There is deliberately no password anywhere in it: what is not here cannot be
// printed, in any format.
type ImageRegistryProbe struct {
	CredentialsEndpoint CredentialsEndpointAnswer `json:"credentialsEndpoint"`
	OlderPath           OlderPathAnswer           `json:"olderPath"`

	// PushPath is the path a push or deploy would take, decided as they decide
	// it. PushPathError is what one would fail with where that is none.
	PushPath      PushPath `json:"pushPath"`
	PushPathError string   `json:"pushPathError,omitempty"`
}

// CredentialsEndpointAnswer is what the registry credentials endpoint
// answered.
type CredentialsEndpointAnswer struct {
	Status       CredentialsEndpointStatus `json:"status"`
	Error        string                    `json:"error,omitempty"`
	RegistryHost string                    `json:"registryHost,omitempty"`
	Repository   string                    `json:"repository,omitempty"`
	Username     string                    `json:"username,omitempty"`

	// ExpiresAt is when the credential stops being accepted, read from the
	// credential itself where it states one: a credential a stack signs for
	// its own registry does, and ECR's token does not.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// OlderPathAnswer is what the older path found.
type OlderPathAnswer struct {
	Status     OlderPathStatus `json:"status"`
	Error      string          `json:"error,omitempty"`
	Repository string          `json:"repository,omitempty"`

	// ExpiresAt is when ECR's authorization token expires, as ECR says.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// ProbeImageRegistry asks both paths to the environment's images and reports
// what each answered.
//
// Both are always asked, and neither can stop the other: a path that fails is
// part of the answer. Which one a push or deploy would take is decided by the
// same rule they use, from what the endpoint answered, so the report cannot
// disagree with the push it explains.
func (target *TargetEnvironment) ProbeImageRegistry() *ImageRegistryProbe {
	probe := &ImageRegistryProbe{}

	credentials, credentialsErr := target.GetRegistryCredentials()
	switch {
	case credentialsErr == nil:
		probe.CredentialsEndpoint = CredentialsEndpointAnswer{
			Status:       CredentialsEndpointServed,
			RegistryHost: credentials.RegistryHost,
			Repository:   credentials.Repository,
			Username:     credentials.Username,
			ExpiresAt:    statedExpiry(credentials.Password),
		}
	case errors.Is(credentialsErr, ErrRegistryCredentialsNotServed):
		probe.CredentialsEndpoint = CredentialsEndpointAnswer{Status: CredentialsEndpointNotServed}
	default:
		probe.CredentialsEndpoint = CredentialsEndpointAnswer{Status: CredentialsEndpointFailed, Error: describe(credentialsErr)}
	}

	repository, expiresAt, olderErr := target.reachOlderPath()
	switch {
	case olderErr == nil:
		probe.OlderPath = OlderPathAnswer{Status: OlderPathReachable, Repository: repository.QualifiedRepository, ExpiresAt: expiresAt}
	case errors.Is(olderErr, errEnvironmentNotDescribed), errors.Is(olderErr, errNoOlderRepository):
		probe.OlderPath = OlderPathAnswer{Status: OlderPathAbsent, Error: olderErr.Error()}
	default:
		probe.OlderPath = OlderPathAnswer{Status: OlderPathFailed, Error: describe(olderErr)}
	}

	probe.PushPath = pushPathFor(credentialsErr)
	switch {
	case probe.PushPath == PushPathNone:
		probe.PushPathError = describe(credentialsErr)
	case probe.PushPath == PushPathOlder && olderErr != nil:
		// A push falls back here and fails here, so this is what it would
		// fail with, and no path is taken.
		probe.PushPath = PushPathNone
		probe.PushPathError = probe.OlderPath.Error
	}
	return probe
}

// statedExpiry is when a credential expires, if it says: a credential a stack
// signs for its own registry is a JWT carrying one. Read without verifying the
// signature, which only the stack can do and which this does not need — it
// reports what the credential claims, and nothing is decided by it.
func statedExpiry(password string) *time.Time {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(password, claims); err != nil {
		return nil
	}
	expiry, err := claims.GetExpirationTime()
	if err != nil || expiry == nil {
		return nil
	}
	return &expiry.Time
}

// describe is an error as the probe reports it. A CLI error's own text is only
// its headline, and whatever wraps one carries just that, so the cause beneath
// it follows.
func describe(err error) string {
	text := err.Error()
	if cliErr, ok := errors.AsType[*clierrors.CLIError](err); ok && cliErr.Cause != nil {
		return text + ": " + describe(cliErr.Cause)
	}
	return text
}
