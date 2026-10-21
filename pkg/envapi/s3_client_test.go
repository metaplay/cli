/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"errors"
	"net/http"
	"testing"
)

// The S3 client is made of AWS credentials the stack issues, which it does
// only for an environment hosted on AWS. A command uploading to the bucket
// tells the user so rather than passing on the stack's 404.

func TestNewS3Client_AnEnvironmentWithNoAWSRegionIsNotOnAWS(t *testing.T) {
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("asked the stack for %s, want no request for an environment with no AWS region", r.URL.Path)
	}))

	_, err := env.NewS3Client(&DeploymentSecret{})

	if !errors.Is(err, ErrNotOnAWS) {
		t.Fatalf("error = %v, want ErrNotOnAWS", err)
	}
}

func TestNewS3Client_AStackIssuingNoAWSCredentialsIsNotOnAWS(t *testing.T) {
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"environment has no AWS credentials to issue: it is not provisioned on AWS"}`, http.StatusNotFound)
	}))

	_, err := env.NewS3Client(&DeploymentSecret{Deployment: Deployment{AwsRegion: "eu-west-1"}})

	if !errors.Is(err, ErrNotOnAWS) {
		t.Fatalf("error = %v, want ErrNotOnAWS", err)
	}
}

// Any other failure stays one: a stack that is down must not read as an
// environment hosted elsewhere.
func TestNewS3Client_OtherFailuresAreNotMistakenForNotOnAWS(t *testing.T) {
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))

	_, err := env.NewS3Client(&DeploymentSecret{Deployment: Deployment{AwsRegion: "eu-west-1"}})

	if err == nil || errors.Is(err, ErrNotOnAWS) {
		t.Fatalf("error = %v, want a failure other than ErrNotOnAWS", err)
	}
}
