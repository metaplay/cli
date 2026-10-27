/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// The S3 client is made of credentials the stack issues for the environment's
// buckets, which not every stack does. A command uploading to a bucket tells
// the user so rather than passing on the stack's 404.

func TestNewS3Client_AnEnvironmentWithNoRegionHasNoStorageCredentials(t *testing.T) {
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("asked the stack for %s, want no request for an environment with no region", r.URL.Path)
	}))

	_, err := env.NewS3Client(&DeploymentSecret{})

	if !errors.Is(err, ErrNoStorageCredentials) {
		t.Fatalf("error = %v, want ErrNoStorageCredentials", err)
	}
}

func TestNewS3Client_AStackIssuingNoCredentialsHasNoStorageCredentials(t *testing.T) {
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"environment has no AWS credentials to issue: it is not provisioned on AWS"}`, http.StatusNotFound)
	}))

	_, err := env.NewS3Client(&DeploymentSecret{Deployment: Deployment{AwsRegion: "eu-west-1"}})

	if !errors.Is(err, ErrNoStorageCredentials) {
		t.Fatalf("error = %v, want ErrNoStorageCredentials", err)
	}
}

// Any other failure stays one: a stack that is down must not read as one that
// issues no credentials.
func TestNewS3Client_OtherFailuresAreNotMistakenForNoStorageCredentials(t *testing.T) {
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))

	_, err := env.NewS3Client(&DeploymentSecret{Deployment: Deployment{AwsRegion: "eu-west-1"}})

	if err == nil || errors.Is(err, ErrNoStorageCredentials) {
		t.Fatalf("error = %v, want a failure other than ErrNoStorageCredentials", err)
	}
}

// On an S3-compatible service, the client addresses the bucket in the path when
// asked to, and sends a plain upload without the checksum trailer that AWS S3
// takes and other services may not.
func TestNewS3Client_UploadsToAnS3CompatibleService(t *testing.T) {
	var gotPath, gotAuthorization, gotTrailer, gotBody string
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPath, gotAuthorization, gotBody = r.URL.Path, r.Header.Get("Authorization"), string(body)
		gotTrailer = r.Header.Get("X-Amz-Trailer") + r.Header.Get("X-Amz-Checksum-Crc32")
	}))
	t.Cleanup(service.Close)

	client, err := newS3Client(&storageAccess{
		Endpoint:     service.URL,
		Region:       "garage",
		UsePathStyle: true,
		Credentials: aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "GKEXAMPLE", SecretAccessKey: "secret"}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String("public"),
		Key:    aws.String("Client/index.html"),
		Body:   strings.NewReader("<html>"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/public/Client/index.html" {
		t.Errorf("path = %q, want the bucket in the path", gotPath)
	}
	if !strings.Contains(gotAuthorization, "Credential=GKEXAMPLE/") || !strings.Contains(gotAuthorization, "/garage/s3/") {
		t.Errorf("Authorization = %q, want it signed with the service's key and region", gotAuthorization)
	}
	if gotTrailer != "" || gotBody != "<html>" {
		t.Errorf("checksum = %q, body = %q; want the plain body and no checksum", gotTrailer, gotBody)
	}
}
