/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
)

// ServerCheck is the result of one check of a game server's status.
type ServerCheck struct {
	Name     string   // What was checked
	Summary  string   // The outcome in a few words, on the same line as Name
	Lines    []string // Details to show below Name, or none
	Problems []string // Why the check failed, one line each, or none when it passed
	Skipped  bool     // Whether the check was skipped, as one that it needs failed
}

// Failed tells whether the check failed.
func (c ServerCheck) Failed() bool {
	return len(c.Problems) > 0
}

// ServerStatus is the state of a game server at one moment. See CheckServerStatus.
type ServerStatus struct {
	Checks        []ServerCheck
	FailedPodLogs []string // The last logs of a pod whose server failed to start, or none
	Notes         []string // Why the logs of a failed pod could not be fetched, or none
	suggestion    string   // What to do when the game server is not ready
}

// Problems returns the problems of all checks, or none when the game server is ready.
func (s ServerStatus) Problems() []string {
	var problems []string
	for _, check := range s.Checks {
		problems = append(problems, check.Problems...)
	}
	return problems
}

// Err returns an error that lists the problems, or nil when the game server is ready.
func (s ServerStatus) Err() error {
	problems := s.Problems()
	if len(problems) == 0 {
		return nil
	}
	return clierrors.New("Game server is not ready").
		WithDetails(problems...).
		WithSuggestion(s.suggestion)
}

// How long each check of CheckServerStatus may take.
const statusCheckTimeout = 10 * time.Second

// How many connections CheckServerStatus makes to the game server's client endpoint.
// Several, to show an endpoint that serves only some of them, such as one with a
// broken load balancer target or address.
const statusClientConnections = 5

// CheckServerStatus checks once whether the game server serves clients and the LiveOps
// Dashboard: the kinds of checks of WaitForServerToBeReady, without waiting. The checks
// run concurrently. The client endpoint check makes statusClientConnections connections,
// where the deploy's wait requires 10 in a row. It returns an error only if the
// checks could not be made, such as when no game server is deployed, or were stopped.
func (targetEnv *TargetEnvironment) CheckServerStatus(ctx context.Context) (ServerStatus, error) {
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return ServerStatus{}, err
	}
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return ServerStatus{}, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	gameServer, err := targetEnv.fetchGameServer(fetchCtx, kubeCli)
	if err != nil {
		if errors.Is(err, ErrNoGameServer) {
			return ServerStatus{}, err
		}
		return ServerStatus{}, clierrors.Wrap(err, "Failed to read the GameServer resource")
	}

	serverHostname := envDetails.Deployment.ServerHostname
	dashboardURL := "https://" + envDetails.Deployment.AdminHostname
	var pods, serverDomain, endpoint, dashboardDomain, dashboard ServerCheck
	var readiness gameServerReadiness
	var podsErr error
	var notes, failedPodLogs []string
	var wg sync.WaitGroup
	wg.Go(func() {
		pods, readiness, podsErr = checkPods(ctx, kubeCli, gameServer)
		if readiness.failedPod != "" {
			logsCtx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
			defer cancel()
			notes, failedPodLogs = failedPodLogLines(logsCtx, kubeCli, readiness.failedPod, readiness.failedPodLogs)
		}
	})
	wg.Go(func() {
		name := fmt.Sprintf("Game server endpoint %s:%d", serverHostname, gameServerClientPort)
		serverDomain, endpoint = checkDomainThen(ctx, "Game server domain", serverHostname, name, func() ServerCheck {
			return checkClientEndpoint(ctx, name, serverHostname, gameServerClientPort)
		})
	})
	wg.Go(func() {
		name := "LiveOps Dashboard " + dashboardURL
		dashboardDomain, dashboard = checkDomainThen(ctx, "LiveOps Dashboard domain", envDetails.Deployment.AdminHostname, name, func() ServerCheck {
			return checkHTTPServer(ctx, name, dashboardURL)
		})
	})
	wg.Wait()

	if ctx.Err() != nil {
		return ServerStatus{}, clierrors.Wrap(ctx.Err(), "Stopped checking the game server status")
	}
	return ServerStatus{
		Checks:        []ServerCheck{pods, serverDomain, endpoint, dashboardDomain, dashboard},
		FailedPodLogs: failedPodLogs,
		Notes:         notes,
		suggestion:    targetEnv.notReadySuggestion(podsErr, readiness, len(failedPodLogs) > 0),
	}, nil
}

// notReadySuggestion tells what to do when the game server is not ready, given the
// error that stopped the check of its pods, their readiness, and whether the logs of a
// failed pod were fetched.
func (targetEnv *TargetEnvironment) notReadySuggestion(podsErr error, readiness gameServerReadiness, hasLogs bool) string {
	switch {
	case podsErr != nil:
		return "The status of the game server pods could not be read from Kubernetes. Run this command again, and if it fails the same way, check that your account can read the environment's pods and stateful sets"
	case readiness.failedPod != "":
		return failedPodSuggestion(targetEnv.HumanID, readiness.failedPod, readiness.failedPodLogs, hasLogs)
	case !readiness.ready():
		return "If a deploy just started, run this command again in a minute. Otherwise, the pod lines above show what blocks it"
	default:
		return "The game server pods are ready, so the failing checks are in DNS or the load balancer. The domain names of a new environment can take up to 15 minutes to resolve"
	}
}

// checkDomainThen checks that hostname resolves, and then makes check, which connects to
// it. If the domain does not resolve, check would fail only for the same reason, so it
// is skipped. name is the name of check.
func checkDomainThen(ctx context.Context, domainName, hostname, name string, check func() ServerCheck) (ServerCheck, ServerCheck) {
	domain := checkDomain(ctx, domainName, hostname)
	if domain.Failed() {
		return domain, ServerCheck{Name: name, Summary: "skipped, as the domain does not resolve", Skipped: true}
	}
	return domain, check()
}

// checkPods checks once whether the game server's pods run the image of its CR and are
// ready. A failed Kubernetes API call fails the check, and is returned.
func checkPods(ctx context.Context, kubeCli *KubeClient, gameServer *TargetGameServer) (ServerCheck, gameServerReadiness, error) {
	check := ServerCheck{Name: "Game server pods"}
	ctx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	target := gameServer.readinessTarget()
	target.now = time.Now()
	readiness, err := checkGameServerReadiness(ctx, kubeCli, gameServer, target)
	if err != nil {
		check.Summary = "failed to check"
		check.Problems = []string{fmt.Sprintf("Failed to check the game server pods: %v", err)}
		return check, readiness, err
	}
	check.Lines = readiness.lines
	check.Problems = readiness.problems
	check.Summary = "ready"
	if !readiness.ready() {
		check.Summary = "not ready"
	}
	return check, readiness, nil
}

// checkDomain checks once that hostname resolves, with lookupDomain.
func checkDomain(ctx context.Context, name, hostname string) ServerCheck {
	check := ServerCheck{Name: fmt.Sprintf("%s %s", name, hostname)}
	ctx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	addrs, err := lookupDomain(ctx, hostname)
	if err != nil {
		check.Summary = "does not resolve"
		check.Problems = []string{fmt.Sprintf("Domain %s does not resolve: %v", hostname, err)}
		return check
	}
	check.Summary = "resolves to " + strings.Join(addrs, ", ")
	return check
}

// checkClientEndpoint connects to the game server's client endpoint
// statusClientConnections times, and checks that each connection succeeds. The
// connections are made one after another, as each looks up the hostname again, and so
// may reach another of the load balancer's addresses. Concurrent lookups of one
// hostname share a single answer.
func checkClientEndpoint(ctx context.Context, name, hostname string, port int) ServerCheck {
	ctx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	numFailed := 0
	var firstErr error
	for range statusClientConnections {
		if err := attemptTLSConnection(ctx, hostname, port); err != nil {
			numFailed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	check := ServerCheck{
		Name:    name,
		Summary: fmt.Sprintf("%d/%d connections succeeded", statusClientConnections-numFailed, statusClientConnections),
	}
	if numFailed > 0 {
		check.Problems = []string{fmt.Sprintf("%d of %d connections to %s:%d failed: %v", numFailed, statusClientConnections, hostname, port, firstErr)}
	}
	return check
}

// checkHTTPServer checks once that url responds with a success or a redirect. The
// request's own timeout limits how long it takes.
func checkHTTPServer(ctx context.Context, name, url string) ServerCheck {
	check := ServerCheck{Name: name, Summary: "does not respond"}
	status, err := probeHTTPServer(ctx, url)
	if status != "" {
		check.Summary = "responds with " + status
	}
	if err != nil {
		if status != "" {
			err = fmt.Errorf("%s: %w", url, err)
		}
		check.Problems = []string{err.Error()}
	}
	return check
}
