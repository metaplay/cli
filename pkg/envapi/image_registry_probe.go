/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	clierrors "github.com/metaplay/cli/internal/errors"
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
	// its own registry does, and ECR's token does not. ExpiryNote says so
	// where it does not, rather than leaving an absence a reader would take
	// for "never".
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	ExpiryNote string     `json:"expiryNote,omitempty"`
}

// OlderPathAnswer is what the older path found.
type OlderPathAnswer struct {
	Status OlderPathStatus `json:"status"`

	// Reason is why there is nothing to reach, where the path is absent;
	// Error is what reaching it failed with, where it failed.
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`

	// Repository is the one the environment's description names, whatever
	// reaching it did.
	Repository string `json:"repository,omitempty"`

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
	probe.PushPath = pushPathFor(credentialsErr)
	switch probe.PushPath {
	case PushPathCredentialsEndpoint:
		probe.CredentialsEndpoint = CredentialsEndpointAnswer{
			Status:       CredentialsEndpointServed,
			RegistryHost: credentials.RegistryHost,
			Repository:   credentials.Repository,
			Username:     credentials.Username,
			ExpiresAt:    statedExpiry(credentials.Password),
		}
		if probe.CredentialsEndpoint.ExpiresAt == nil {
			probe.CredentialsEndpoint.ExpiryNote = unstatedExpiryNote(credentials.RegistryHost)
		}
	case PushPathOlder:
		probe.CredentialsEndpoint = CredentialsEndpointAnswer{Status: CredentialsEndpointNotServed}
	default:
		failure := errorChainText(credentialsErr)
		probe.CredentialsEndpoint = CredentialsEndpointAnswer{Status: CredentialsEndpointFailed, Error: failure}
		probe.PushPathError = failure
	}

	older, olderErr := target.reachOlderPath()
	probe.OlderPath = OlderPathAnswer{Repository: older.namedRepository}
	switch {
	case olderErr == nil:
		probe.OlderPath.Status = OlderPathReachable
		probe.OlderPath.ExpiresAt = older.expiresAt
	case errors.Is(olderErr, errEnvironmentNotDescribed), errors.Is(olderErr, errNoOlderRepository):
		probe.OlderPath.Status = OlderPathAbsent
		probe.OlderPath.Reason = olderErr.Error()
	default:
		probe.OlderPath.Status = OlderPathFailed
		probe.OlderPath.Error = errorChainText(olderErr)
	}

	// A push that falls back to the older path and cannot reach it fails
	// there, with what ResolveImageRepository makes of that failure, and no
	// path is taken.
	if probe.PushPath == PushPathOlder && olderErr != nil {
		probe.PushPath = PushPathNone
		probe.PushPathError = errorChainText(target.olderPathFailure(olderErr))
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

// unstatedExpiryNote says a credential states no expiry, and, for ECR, what
// ECR documents instead.
func unstatedExpiryNote(registryHost string) string {
	if isECRRegistryHost(registryHost) {
		return "not stated by the credential; an ECR authorization token is valid for 12 hours"
	}
	return "not stated by the credential"
}

// isECRRegistryHost is whether a registry host is ECR's:
// <account>.dkr.ecr.<region>.amazonaws.com, or .amazonaws.com.cn in China.
func isECRRegistryHost(host string) bool {
	return strings.Contains(host, ".dkr.ecr.") &&
		(strings.HasSuffix(host, ".amazonaws.com") || strings.HasSuffix(host, ".amazonaws.com.cn"))
}

// errorChainText is an error as the probe reports it. A CLI error's own text
// is only its headline, and whatever wraps one carries just that, so the cause
// beneath it follows.
func errorChainText(err error) string {
	text := err.Error()
	if cliErr, ok := clierrors.AsCLIError(err); ok && cliErr.Cause != nil {
		return text + ": " + errorChainText(cliErr.Cause)
	}
	return text
}
