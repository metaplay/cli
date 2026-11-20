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

// CheckResult is the outcome of one check of CheckServerStatus.
type CheckResult int

const (
	CheckPassed  CheckResult = iota
	CheckFailed              // See ServerCheck.Problems
	CheckSkipped             // An earlier check that this one needs failed
)

// ServerCheck is the result of one check of a game server's status.
type ServerCheck struct {
	Name     string      // What was checked
	Result   CheckResult // Whether it passed
	Summary  string      // The outcome in a few words, on the same line as Name
	Lines    []string    // Details to show below Name, or none
	Problems []string    // Why the check failed, one line each, or none
}

// ServerStatus is the state of a game server at one moment. See CheckServerStatus.
type ServerStatus struct {
	Checks          []ServerCheck
	FailedPod       string // Name of a pod whose server failed to start, or empty
	FailedPodHasLog bool   // Whether FailedPod's server container has logs
}

// Problems returns the problems of all checks, or none when the game server is ready.
func (s ServerStatus) Problems() []string {
	var problems []string
	for _, check := range s.Checks {
		problems = append(problems, check.Problems...)
	}
	return problems
}

// How long each check of CheckServerStatus may take.
const statusCheckTimeout = 10 * time.Second

// How many connections CheckServerStatus makes to the game server's client endpoint.
// Several, to show an endpoint that serves only some of them, such as one with a
// broken load balancer target.
const statusClientConnections = 5

// CheckServerStatus checks once whether the game server serves clients and the LiveOps
// Dashboard: the same checks as WaitForServerToBeReady, without waiting. The checks run
// concurrently. It returns an error only if the checks could not be started, such as
// when no game server is deployed.
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

	var status ServerStatus
	var podsCheck, serverChecks, dashboardChecks []ServerCheck
	var wg sync.WaitGroup
	wg.Go(func() {
		check, readiness := checkPods(ctx, kubeCli, gameServer)
		podsCheck = []ServerCheck{check}
		status.FailedPod = readiness.failedPod
		status.FailedPodHasLog = readiness.failedPodLogs != noPodLogs
	})
	serverHostname := envDetails.Deployment.ServerHostname
	wg.Go(func() {
		dns := checkDomain(ctx, "Game server domain", serverHostname)
		name := fmt.Sprintf("Game server endpoint %s:%d", serverHostname, gameServerClientPort)
		endpoint := checkAfterDomain(dns, name, func() ServerCheck {
			return checkClientEndpoint(ctx, name, serverHostname, gameServerClientPort)
		})
		serverChecks = []ServerCheck{dns, endpoint}
	})
	adminHostname := envDetails.Deployment.AdminHostname
	wg.Go(func() {
		dns := checkDomain(ctx, "LiveOps Dashboard domain", adminHostname)
		url := "https://" + adminHostname
		name := "LiveOps Dashboard " + url
		dashboard := checkAfterDomain(dns, name, func() ServerCheck {
			return checkHTTPServer(ctx, name, url)
		})
		dashboardChecks = []ServerCheck{dns, dashboard}
	})
	wg.Wait()

	status.Checks = append(append(podsCheck, serverChecks...), dashboardChecks...)
	return status, nil
}

// checkAfterDomain runs check, which connects to a domain, if the check of the domain
// passed. Otherwise, it would fail only for the same reason, so it is skipped.
func checkAfterDomain(domainCheck ServerCheck, name string, check func() ServerCheck) ServerCheck {
	if domainCheck.Result != CheckPassed {
		return ServerCheck{Name: name, Result: CheckSkipped, Summary: "skipped, as the domain does not resolve"}
	}
	return check()
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
		check.Result = CheckFailed
		check.Summary = "failed to check"
		check.Problems = []string{fmt.Sprintf("Failed to check the game server pods: %v", err)}
		return check, readiness
	}
	check.Lines = readiness.lines
	check.Problems = readiness.problems
	if readiness.ready() {
		check.Summary = "ready"
	} else {
		check.Result = CheckFailed
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
		check.Result = CheckFailed
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
	check := ServerCheck{Name: name}
	ctx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	errs := make([]error, statusClientConnections)
	var wg sync.WaitGroup
	for ndx := range errs {
		wg.Go(func() { errs[ndx] = attemptTLSConnection(ctx, hostname, port) })
	}
	wg.Wait()

	numSucceeded := 0
	for _, err := range errs {
		if err == nil {
			numSucceeded++
		}
	}
	check.Summary = fmt.Sprintf("%d/%d connections succeeded", numSucceeded, statusClientConnections)
	if numSucceeded < statusClientConnections {
		check.Result = CheckFailed
		check.Problems = []string{fmt.Sprintf("%d of %d connections to %s:%d failed: %v", statusClientConnections-numSucceeded, statusClientConnections, hostname, port, firstError(errs))}
	}
	return check
}

// checkHTTPServer checks once that url responds with a success or a redirect.
func checkHTTPServer(ctx context.Context, name, url string) ServerCheck {
	check := ServerCheck{Name: name}
	ctx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	status, err := probeHTTPServer(ctx, url)
	if err != nil {
		check.Result = CheckFailed
		check.Summary = "does not respond"
		if status != "" {
			check.Summary = "responds with " + status
		}
		check.Problems = []string{fmt.Sprintf("%s: %v", url, err)}
		return check
	}
	check.Summary = "responds with " + status
	return check
}

// firstError returns the first non-nil error of errs, or nil.
func firstError(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
