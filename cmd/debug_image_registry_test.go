/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/metahttp"
)

// probedFrom is what the probe reports against a stack that issues credential
// and describes no environment, so the older path finds nothing.
func probedFrom(t *testing.T, credential envapi.RegistryCredentials) *envapi.ImageRegistryProbe {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/registry") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(credential)
	}))
	t.Cleanup(server.Close)

	tokenSet := &auth.TokenSet{}
	env := &envapi.TargetEnvironment{
		TokenSet:        tokenSet,
		StackApiBaseURL: server.URL,
		HumanID:         "lovely-wombats-build-nimbly",
		StackApiClient:  metahttp.NewJSONClient(tokenSet, server.URL),
	}
	return env.ProbeImageRegistry()
}

// The credential is what the command is about, and the one thing it must never
// show: printed, it is a working push credential in a terminal's scrollback or
// a CI log.
func TestFormatImageRegistryProbe_NeverShowsThePassword(t *testing.T) {
	now := time.Date(2030, 6, 7, 8, 0, 0, 0, time.UTC)
	password, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "developer",
		"exp": now.Add(30 * time.Minute).Unix(),
	}).SignedString([]byte("a key"))
	if err != nil {
		t.Fatal(err)
	}
	probe := probedFrom(t, envapi.RegistryCredentials{
		RegistryHost: "registry.example-stack.example.com",
		Repository:   "lovely-wombats-build-nimbly/gameserver",
		Username:     "developer",
		Password:     password,
	})
	_, payload, _ := strings.Cut(password, ".")

	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			output, err := formatImageRegistryProbe(probe, format, now)
			if err != nil {
				t.Fatal(err)
			}

			if strings.Contains(output, password) || strings.Contains(output, payload) {
				t.Errorf("the output carries the credential:\n%s", output)
			}
			// And it is a report of something: the control that makes the
			// absence above mean anything.
			if !strings.Contains(output, "registry.example-stack.example.com") {
				t.Errorf("the output does not name the registry the stack issued for:\n%s", output)
			}
		})
	}
}

// What a developer returning to a refused deploy needs: when the credential
// stopped being accepted, against the clock they are reading it by.
func TestFormatImageRegistryProbe_SaysWhenTheCredentialExpires(t *testing.T) {
	now := time.Date(2030, 6, 7, 8, 0, 0, 0, time.UTC)
	expired := envapi.ImageRegistryProbe{
		CredentialsEndpoint: envapi.CredentialsEndpointAnswer{
			Status:       envapi.CredentialsEndpointServed,
			RegistryHost: "registry.example-stack.example.com",
			Repository:   "lovely-wombats-build-nimbly/gameserver",
			Username:     "developer",
			ExpiresAt:    new(now.Add(-5 * time.Minute)),
		},
		OlderPath: envapi.OlderPathAnswer{Status: envapi.OlderPathAbsent, Error: "the stack has no description of this environment"},
		PushPath:  envapi.PushPathCredentialsEndpoint,
	}

	output, err := formatImageRegistryProbe(&expired, "text", now)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(output, "2030-06-07T07:55:00Z") || !strings.Contains(output, "expired 5m0s ago") {
		t.Errorf("the output does not say the credential expired five minutes ago:\n%s", output)
	}
}

// An ECR token says nothing of its own expiry. The output says so, rather than
// leaving a blank a reader would take for "never".
func TestFormatImageRegistryProbe_SaysWhenACredentialStatesNoExpiry(t *testing.T) {
	unstated := envapi.ImageRegistryProbe{
		CredentialsEndpoint: envapi.CredentialsEndpointAnswer{
			Status:       envapi.CredentialsEndpointServed,
			RegistryHost: "123456789012.dkr.ecr.eu-west-1.amazonaws.com",
			Repository:   "lovely-wombats-build-nimbly",
			Username:     "AWS",
		},
		OlderPath: envapi.OlderPathAnswer{Status: envapi.OlderPathAbsent, Error: "the environment names no repository of its own"},
		PushPath:  envapi.PushPathCredentialsEndpoint,
	}

	output, err := formatImageRegistryProbe(&unstated, "text", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(output, "not stated by the credential") {
		t.Errorf("the output does not say the expiry is unstated:\n%s", output)
	}
}
