/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	Checks     []ServerCheck
	suggestion string // What to do when the game server is not ready
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
// broken load balancer target.
const statusClientConnections = 5

// CheckServerStatus checks once whether the game server serves clients and the LiveOps
// Dashboard: the checks of WaitForServerToBeReady, without waiting. The checks run
// concurrently. The client endpoint check makes statusClientConnections connections at
// once, where the deploy's wait requires 10 in a row. It returns an error only if the
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
	gameServer, err := targetEnv.fetchGameServer(ctx, kubeCli)
	if err != nil {
		if errors.Is(err, errNoGameServer) {
			return ServerStatus{}, err
		}
		return ServerStatus{}, clierrors.Wrap(err, "Failed to read the GameServer resource")
	}

	status := ServerStatus{Checks: make([]ServerCheck, 5)}
	serverHostname := envDetails.Deployment.ServerHostname
	dashboardURL := "https://" + envDetails.Deployment.AdminHostname
	var wg sync.WaitGroup
	wg.Go(func() {
		var readiness gameServerReadiness
		status.Checks[0], readiness = checkPods(ctx, kubeCli, gameServer)
		status.suggestion = targetEnv.notReadySuggestion(readiness)
	})
	wg.Go(func() {
		name := fmt.Sprintf("Game server endpoint %s:%d", serverHostname, gameServerClientPort)
		status.Checks[1], status.Checks[2] = checkDomainThen(ctx, "Game server domain", serverHostname, name, func() ServerCheck {
			return checkClientEndpoint(ctx, name, serverHostname, gameServerClientPort)
		})
	})
	wg.Go(func() {
		name := "LiveOps Dashboard " + dashboardURL
		status.Checks[3], status.Checks[4] = checkDomainThen(ctx, "LiveOps Dashboard domain", envDetails.Deployment.AdminHostname, name, func() ServerCheck {
			return checkHTTPServer(ctx, name, dashboardURL)
		})
	})
	wg.Wait()

	if ctx.Err() != nil {
		return ServerStatus{}, clierrors.Wrap(ctx.Err(), "Stopped checking the game server status")
	}
	return status, nil
}

// notReadySuggestion tells what to do when the game server is not ready, given the
// readiness of its pods.
func (targetEnv *TargetEnvironment) notReadySuggestion(readiness gameServerReadiness) string {
	switch {
	case readiness.failedPod == "":
		return fmt.Sprintf("Run this command again to see whether the game server is still starting, or check the server logs with: metaplay debug logs %s", targetEnv.HumanID)
	case readiness.failedPodLogs == noPodLogs:
		return fmt.Sprintf("The server container of pod %s did not start, so it has no logs. Fix the cause above, and deploy again", readiness.failedPod)
	default:
		return fmt.Sprintf("Check the logs of the failed pod with: metaplay debug logs %s --pod %s", targetEnv.HumanID, readiness.failedPod)
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
// ready. A failed Kubernetes API call fails the check.
func checkPods(ctx context.Context, kubeCli *KubeClient, gameServer *TargetGameServer) (ServerCheck, gameServerReadiness) {
	check := ServerCheck{Name: "Game server pods"}
	ctx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	target := gameServer.readinessTarget()
	target.now = time.Now()
	readiness, err := checkGameServerReadiness(ctx, kubeCli, gameServer, target)
	if err != nil {
		check.Summary = "failed to check"
		check.Problems = []string{fmt.Sprintf("Failed to check the game server pods: %v", err)}
		return check, readiness
	}
	check.Lines = readiness.lines
	check.Problems = readiness.problems
	check.Summary = "ready"
	if !readiness.ready() {
		check.Summary = "not ready"
	}
	return check, readiness
}

// checkDomain checks once that hostname resolves.
func checkDomain(ctx context.Context, name, hostname string) ServerCheck {
	check := ServerCheck{Name: fmt.Sprintf("%s %s", name, hostname)}
	ctx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, hostname)
	if err != nil {
		check.Summary = "does not resolve"
		check.Problems = []string{fmt.Sprintf("Domain %s does not resolve: %v", hostname, err)}
		return check
	}
	check.Summary = "resolves to " + strings.Join(addrs, ", ")
	return check
}

// checkClientEndpoint connects to the game server's client endpoint
// statusClientConnections times at once, and checks that each connection succeeds.
func checkClientEndpoint(ctx context.Context, name, hostname string, port int) ServerCheck {
	ctx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	errs := make([]error, statusClientConnections)
	var wg sync.WaitGroup
	for ndx := range errs {
		wg.Go(func() { errs[ndx] = attemptTLSConnection(ctx, hostname, port) })
	}
	wg.Wait()

	numFailed := 0
	var firstErr error
	for _, err := range errs {
		if err != nil {
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
		check.Problems = []string{fmt.Sprintf("%s: %v", url, err)}
	}
	return check
}
