/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// The probe asks both paths to an environment's images and reports what each
// answered. Neither answer may stop it: a path that fails is part of the
// report, which is the whole reason the probe exists.

// stackAnswers is how a fake stack answers each request the probe makes.
// A zero status means 200 with the given body.
type stackAnswers struct {
	registryStatus int
	registry       RegistryCredentials
	detailsStatus  int
	ecrRepo        string
	awsStatus      int
}

// stackWithECR is a fake stack answering as told, and a fake ECR answering
// ecrStatus (0 for a token), wired up the way a real ECR is reached: through
// the endpoint the AWS SDK is pointed at.
func stackWithECR(t *testing.T, answers stackAnswers, ecrStatus int) *TargetEnvironment {
	t.Helper()

	ecr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		if ecrStatus != 0 {
			w.WriteHeader(ecrStatus)
			_, _ = w.Write([]byte(`{"__type":"AccessDeniedException","message":"not authorized to call GetAuthorizationToken"}`))
			return
		}
		token := base64.StdEncoding.EncodeToString([]byte("AWS:ecr-password"))
		_, _ = fmt.Fprintf(w, `{"authorizationData":[{"authorizationToken":%q,"expiresAt":%d,"proxyEndpoint":"https://123456789012.dkr.ecr.eu-west-1.amazonaws.com"}]}`,
			token, ecrExpiry.Unix())
	}))
	t.Cleanup(ecr.Close)
	t.Setenv("AWS_ENDPOINT_URL_ECR", ecr.URL)

	return testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		answer := func(status int, body any) {
			if status != 0 {
				http.Error(w, `{"error":"refused"}`, status)
				return
			}
			_ = json.NewEncoder(w).Encode(body)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/registry"):
			answer(answers.registryStatus, answers.registry)
		case strings.HasSuffix(r.URL.Path, "/aws"):
			answer(answers.awsStatus, AWSCredentials{AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "secret", SessionToken: "session"})
		case strings.HasPrefix(r.URL.Path, "/v0/deployments/"):
			details := DeploymentSecret{}
			details.Deployment.EcrRepo = answers.ecrRepo
			details.Deployment.AwsRegion = "eu-west-1"
			answer(answers.detailsStatus, details)
		default:
			http.NotFound(w, r)
		}
	}))
}

var ecrExpiry = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

const olderRepository = "123456789012.dkr.ecr.eu-west-1.amazonaws.com/lovely-wombats-build-nimbly"

// signedCredential is a credential as a stack signs one for its own registry:
// a JWT whose expiry the probe reads without verifying it.
func signedCredential(t *testing.T, expires time.Time) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "developer",
		"exp": expires.Unix(),
	}).SignedString([]byte("not the stack's key; the probe does not verify"))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func servedCredential(password string) RegistryCredentials {
	return RegistryCredentials{
		RegistryHost: "registry.example-stack.example.com",
		Repository:   "lovely-wombats-build-nimbly/gameserver",
		Username:     "developer",
		Password:     password,
	}
}

// A stack serving the endpoint: what it issued, when that expires, and the
// older path asked all the same — on a stack whose environment names no cloud
// repository, there is none.
func TestProbeImageRegistry_AStackServingTheEndpoint(t *testing.T) {
	expires := time.Date(2030, 6, 7, 8, 9, 10, 0, time.UTC)
	env := stackWithECR(t, stackAnswers{registry: servedCredential(signedCredential(t, expires))}, 0)

	probe := env.ProbeImageRegistry()

	endpoint := probe.CredentialsEndpoint
	if endpoint.Status != CredentialsEndpointServed {
		t.Fatalf("endpoint status = %q, want served: %+v", endpoint.Status, endpoint)
	}
	if endpoint.RegistryHost != "registry.example-stack.example.com" ||
		endpoint.Repository != "lovely-wombats-build-nimbly/gameserver" ||
		endpoint.Username != "developer" {
		t.Errorf("endpoint answer = %+v", endpoint)
	}
	if endpoint.ExpiresAt == nil || !endpoint.ExpiresAt.Equal(expires) {
		t.Errorf("expiry = %v, want %v, read from the credential itself", endpoint.ExpiresAt, expires)
	}
	if probe.OlderPath.Status != OlderPathAbsent {
		t.Errorf("older path = %+v, want absent: the environment names no cloud repository", probe.OlderPath)
	}
	if probe.PushPath != PushPathCredentialsEndpoint {
		t.Errorf("push path = %q, want the credentials endpoint", probe.PushPath)
	}
}

// An environment in ECR on a stack serving the endpoint answers both ways,
// and a push takes the endpoint.
func TestProbeImageRegistry_BothPathsAnswering(t *testing.T) {
	env := stackWithECR(t, stackAnswers{
		registry: RegistryCredentials{
			RegistryHost: "123456789012.dkr.ecr.eu-west-1.amazonaws.com",
			Repository:   "lovely-wombats-build-nimbly",
			Username:     "AWS",
			Password:     "an-ecr-token-that-is-not-a-jwt",
		},
		ecrRepo: olderRepository,
	}, 0)

	probe := env.ProbeImageRegistry()

	if probe.CredentialsEndpoint.Status != CredentialsEndpointServed {
		t.Fatalf("endpoint = %+v, want served", probe.CredentialsEndpoint)
	}
	if probe.CredentialsEndpoint.ExpiresAt != nil {
		t.Errorf("expiry = %v, want none: an ECR token does not state one", probe.CredentialsEndpoint.ExpiresAt)
	}
	older := probe.OlderPath
	if older.Status != OlderPathReachable {
		t.Fatalf("older path = %+v, want reachable", older)
	}
	if older.Repository != olderRepository {
		t.Errorf("older path repository = %q, want the one the environment names", older.Repository)
	}
	if older.ExpiresAt == nil || !older.ExpiresAt.Equal(ecrExpiry) {
		t.Errorf("older path expiry = %v, want ECR's own %v", older.ExpiresAt, ecrExpiry)
	}
	if probe.PushPath != PushPathCredentialsEndpoint {
		t.Errorf("push path = %q, want the endpoint, which wins whenever it is served", probe.PushPath)
	}
}

// A stack older than the endpoint: the endpoint is not served, and a push
// falls back to the older path, which answers.
func TestProbeImageRegistry_AStackOlderThanTheEndpoint(t *testing.T) {
	env := stackWithECR(t, stackAnswers{registryStatus: http.StatusNotFound, ecrRepo: olderRepository}, 0)

	probe := env.ProbeImageRegistry()

	if probe.CredentialsEndpoint.Status != CredentialsEndpointNotServed {
		t.Errorf("endpoint = %+v, want not served", probe.CredentialsEndpoint)
	}
	if probe.OlderPath.Status != OlderPathReachable {
		t.Errorf("older path = %+v, want reachable", probe.OlderPath)
	}
	if probe.PushPath != PushPathOlder {
		t.Errorf("push path = %q, want the older path", probe.PushPath)
	}
}

// The failures are the answer. None of them stops the probe, and each says
// what failed — and where no path would be taken, what a push would fail with,
// in exactly the words a push would use.
func TestProbeImageRegistry_FailuresAreReportedRatherThanReturned(t *testing.T) {
	for name, tc := range map[string]struct {
		answers      stackAnswers
		ecrStatus    int
		wantEndpoint CredentialsEndpointStatus
		wantOlder    OlderPathStatus
		wantPush     PushPath
	}{
		"the endpoint fails, so a push stops there": {
			answers:      stackAnswers{registryStatus: http.StatusInternalServerError, ecrRepo: olderRepository},
			wantEndpoint: CredentialsEndpointFailed,
			wantOlder:    OlderPathReachable,
			wantPush:     PushPathNone,
		},
		"the endpoint fails, and there is no older path either": {
			answers:      stackAnswers{registryStatus: http.StatusInternalServerError},
			wantEndpoint: CredentialsEndpointFailed,
			wantOlder:    OlderPathAbsent,
			wantPush:     PushPathNone,
		},
		"both fail": {
			answers:      stackAnswers{registryStatus: http.StatusInternalServerError, ecrRepo: olderRepository, awsStatus: http.StatusForbidden},
			wantEndpoint: CredentialsEndpointFailed,
			wantOlder:    OlderPathFailed,
			wantPush:     PushPathNone,
		},
		"served, and the older path fails, which a push never reaches": {
			answers:      stackAnswers{registry: servedCredential("a-password"), ecrRepo: olderRepository, awsStatus: http.StatusForbidden},
			wantEndpoint: CredentialsEndpointServed,
			wantOlder:    OlderPathFailed,
			wantPush:     PushPathCredentialsEndpoint,
		},
		"not served, and the stack refuses cloud credentials": {
			answers:      stackAnswers{registryStatus: http.StatusNotFound, ecrRepo: olderRepository, awsStatus: http.StatusForbidden},
			wantEndpoint: CredentialsEndpointNotServed,
			wantOlder:    OlderPathFailed,
			wantPush:     PushPathNone,
		},
		"not served, and ECR refuses the credentials the stack issued": {
			answers:      stackAnswers{registryStatus: http.StatusNotFound, ecrRepo: olderRepository},
			ecrStatus:    http.StatusBadRequest,
			wantEndpoint: CredentialsEndpointNotServed,
			wantOlder:    OlderPathFailed,
			wantPush:     PushPathNone,
		},
		"not served, and the environment names no repository": {
			answers:      stackAnswers{registryStatus: http.StatusNotFound},
			wantEndpoint: CredentialsEndpointNotServed,
			wantOlder:    OlderPathAbsent,
			wantPush:     PushPathNone,
		},
		"not served, and the stack does not describe the environment": {
			answers:      stackAnswers{registryStatus: http.StatusNotFound, detailsStatus: http.StatusNotFound},
			wantEndpoint: CredentialsEndpointNotServed,
			wantOlder:    OlderPathAbsent,
			wantPush:     PushPathNone,
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := stackWithECR(t, tc.answers, tc.ecrStatus)

			probe := env.ProbeImageRegistry()

			if probe.CredentialsEndpoint.Status != tc.wantEndpoint {
				t.Errorf("endpoint = %+v, want %q", probe.CredentialsEndpoint, tc.wantEndpoint)
			}
			if tc.wantEndpoint == CredentialsEndpointFailed && probe.CredentialsEndpoint.Error == "" {
				t.Errorf("endpoint failed with no error to show")
			}

			older := probe.OlderPath
			if older.Status != tc.wantOlder {
				t.Errorf("older path = %+v, want %q", older, tc.wantOlder)
			}
			switch tc.wantOlder {
			case OlderPathAbsent:
				if older.Reason == "" {
					t.Errorf("older path is absent with nothing saying why")
				}
			case OlderPathFailed:
				if older.Error == "" {
					t.Errorf("older path failed with no error to show")
				}
			}
			if tc.answers.ecrRepo != "" && tc.answers.detailsStatus == 0 && older.Repository != tc.answers.ecrRepo {
				t.Errorf("older path repository = %q, want the %q the environment names, whatever reaching it did", older.Repository, tc.answers.ecrRepo)
			}

			if probe.PushPath != tc.wantPush {
				t.Errorf("push path = %q, want %q", probe.PushPath, tc.wantPush)
			}
			if tc.wantPush == PushPathNone {
				// The claim is that this is what a push would fail with, so it
				// is held to what a push does fail with.
				_, pushErr := env.ResolveImageRepository()
				if pushErr == nil {
					t.Fatalf("the probe says no path would be taken, and resolving one succeeded")
				}
				if probe.PushPathError != errorChainText(pushErr) {
					t.Errorf("push path error = %q, but a push fails with %q", probe.PushPathError, errorChainText(pushErr))
				}
			}
		})
	}
}

// A credential that states no expiry is said to state none, in the data as
// well as in the text, and ECR's documented validity is offered only for ECR.
func TestProbeImageRegistry_ACredentialStatingNoExpirySaysSo(t *testing.T) {
	for name, tc := range map[string]struct {
		host     string
		password string
		wantECR  bool
	}{
		"an ECR token": {host: "123456789012.dkr.ecr.eu-west-1.amazonaws.com", password: "an-ecr-token", wantECR: true},
		"a signed credential without an expiry": {host: "registry.example-stack.example.com", password: func() string {
			signed, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "developer"}).SignedString([]byte("k"))
			return signed
		}()},
	} {
		t.Run(name, func(t *testing.T) {
			credential := servedCredential(tc.password)
			credential.RegistryHost = tc.host
			env := stackWithECR(t, stackAnswers{registry: credential}, 0)

			endpoint := env.ProbeImageRegistry().CredentialsEndpoint

			if endpoint.ExpiresAt != nil {
				t.Fatalf("expiry = %v, want none stated", endpoint.ExpiresAt)
			}
			if !strings.Contains(endpoint.ExpiryNote, "not stated by the credential") {
				t.Errorf("expiry note = %q, want it to say the credential states none", endpoint.ExpiryNote)
			}
			if mentionsECR := strings.Contains(endpoint.ExpiryNote, "12 hours"); mentionsECR != tc.wantECR {
				t.Errorf("expiry note = %q; ECR's validity offered = %v, want %v", endpoint.ExpiryNote, mentionsECR, tc.wantECR)
			}
		})
	}
}

// An older path with nothing to reach is an ordinary answer — it is what an
// environment whose images are not in ECR looks like — so finding it is not
// logged as a failed request. On a stack that keeps no such descriptions, every
// run of the command would otherwise open with an error line.
func TestProbeImageRegistry_AnAbsentOlderPathIsNotLoggedAsAFailure(t *testing.T) {
	var logged strings.Builder
	previousLogger := log.Logger
	log.Logger = zerolog.New(&logged)
	t.Cleanup(func() { log.Logger = previousLogger })
	// Not found as a route that does not exist answers it: plain text rather
	// than the stack's structured error, which is the body a failed request
	// is logged loudest for.
	env := testEnvironment(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/registry") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(servedCredential("a-password"))
	}))

	probe := env.ProbeImageRegistry()

	if probe.OlderPath.Status != OlderPathAbsent {
		t.Fatalf("older path = %+v, want absent", probe.OlderPath)
	}
	if strings.Contains(logged.String(), `"level":"error"`) {
		t.Errorf("an absent older path was logged as an error:\n%s", logged.String())
	}
}
