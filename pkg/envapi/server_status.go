/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
)

// ServerCheck is the result of one check of a game server's status.
type ServerCheck struct {
	Name     string   // What was checked
	Summary  string   // The outcome in a few words, on the same line as Name
	Lines    []string // Details to show below Name, or none
	Problems []string // Why the check failed, one line each, or none when it passed
	Skipped  bool     // Whether the check was skipped, as one that it needs failed
	hint     string   // What to do when the check failed although the pods are ready
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

// Err returns an error that lists the problems of all checks, or nil when the game
// server is ready.
func (s ServerStatus) Err() error {
	var problems []string
	for _, check := range s.Checks {
		problems = append(problems, check.Problems...)
	}
	if len(problems) == 0 {
		return nil
	}
	return clierrors.New("Game server is not ready").
		WithDetails(problems...).
		WithSuggestion(s.suggestion)
}

// What failed, when the GameServer resource or the game server pods could not be read.
// Shared by CheckServerStatus and the wait of a deploy, so that both name a failure alike.
const (
	readGameServerFailure = "Failed to read the GameServer resource"
	checkPodsFailure      = "Failed to check the game server pods"
)

// How long one attempt of a check may take, unless the check sets its own limit.
const attemptTimeout = 10 * time.Second

// How long the wait of a deploy waits for a domain to resolve. The domain names of a
// new environment take a while to propagate.
const domainTimeout = 15 * time.Minute

// How long the wait of a deploy waits for the client endpoint and the LiveOps Dashboard
// to respond, once their domains resolve.
const serviceTimeout = 5 * time.Minute

// How many connections the check of the game server's client endpoint makes. All are
// made and counted, to show an endpoint that serves only some of them, such as one with
// a broken load balancer target or address. The check passes only if all succeed.
const clientConnections = 10

// How long one connection of the check of the client endpoint may take: its TLS
// handshake, sending a HealthCheck and receiving the protocol header. That is 4 round
// trips, so a client far from the environment has over 1 second for each.
const connectionTimeout = 5 * time.Second

// networkCheck declares one check of a game server's networking, which CheckServerStatus
// makes once and WaitForServerToBeReady waits for. See networkChecks.
type networkCheck struct {
	name       string                                // What is checked, shown by CheckServerStatus, and by the wait once it passes
	title      string                                // Title of the wait's task
	waitingFor string                                // What the wait waits for, such as "domain x to resolve", for its header and errors
	timeout    time.Duration                         // How long the wait waits for the check to pass
	interval   time.Duration                         // How long the wait pauses between two attempts
	maxAttempt time.Duration                         // How long one attempt may take, or zero for attemptTimeout
	hint       string                                // What to do when the check fails although the pods are ready
	probe      func(ctx context.Context) ServerCheck // Makes the check once. Fills in all but Name.
}

// domainChecks is the check that a domain resolves, and the check of the service at the
// domain. The service is checked only once the domain resolves: it would fail only for
// the same reason.
type domainChecks struct {
	domain  networkCheck
	service networkCheck
}

// networkChecks declares the checks of a game server's networking, in the order in
// which WaitForServerToBeReady waits for them, after the pods.
func networkChecks(envDetails *DeploymentSecret) []domainChecks {
	serverHostname := envDetails.Deployment.ServerHostname
	adminHostname := envDetails.Deployment.AdminHostname
	endpoint := fmt.Sprintf("%s:%d", serverHostname, gameServerClientPort)
	dashboardURL := "https://" + adminHostname
	return []domainChecks{
		{
			domain: domainCheck("Game server domain", "Wait for game server domain name to propagate", serverHostname),
			service: networkCheck{
				name:       "Game server endpoint " + endpoint,
				title:      "Wait for game server to serve clients",
				waitingFor: fmt.Sprintf("game server endpoint %s to be ready", endpoint),
				timeout:    serviceTimeout,
				interval:   1 * time.Second,
				maxAttempt: clientConnections * connectionTimeout,
				hint:       "The game server pods are ready, but the load balancer does not route all client connections to them. Right after a deploy, it can take a minute to route connections to new pods, so run this command again shortly",
				probe: func(ctx context.Context) ServerCheck {
					return checkClientEndpoint(ctx, serverHostname, gameServerClientPort)
				},
			},
		},
		{
			domain: domainCheck("LiveOps Dashboard domain", "Wait for LiveOps Dashboard domain name to propagate", adminHostname),
			service: networkCheck{
				name:       "LiveOps Dashboard " + dashboardURL,
				title:      "Wait for LiveOps Dashboard to serve traffic",
				waitingFor: fmt.Sprintf("LiveOps Dashboard %s to respond", dashboardURL),
				timeout:    serviceTimeout,
				interval:   1 * time.Second,
				hint:       "The game server pods are ready, but the request to the LiveOps Dashboard failed. Right after a deploy, run this command again in a minute. Otherwise, the error above tells whether its certificate, its ingress or its authentication fails",
				probe: func(ctx context.Context) ServerCheck {
					return checkHTTPServer(ctx, dashboardURL)
				},
			},
		},
	}
}

// domainCheck declares the check that hostname resolves. name tells whose domain it is.
func domainCheck(name, title, hostname string) networkCheck {
	return networkCheck{
		name:       name + " " + hostname,
		title:      title,
		waitingFor: fmt.Sprintf("domain %s to resolve", hostname),
		timeout:    domainTimeout,
		interval:   5 * time.Second, // Propagation takes a while
		hint:       fmt.Sprintf("The game server pods are ready, but domain %s does not resolve. The domain names of a new environment can take up to %d minutes to resolve, so run this command again later", hostname, int(domainTimeout.Minutes())),
		probe: func(ctx context.Context) ServerCheck {
			return checkDomain(ctx, hostname)
		},
	}
}

// checkOnce makes the check once, in at most c.maxAttempt. A failed check carries c.hint.
func (c networkCheck) checkOnce(ctx context.Context) ServerCheck {
	maxAttempt := c.maxAttempt
	if maxAttempt == 0 {
		maxAttempt = attemptTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, maxAttempt)
	defer cancel()
	check := c.probe(ctx)
	check.Name = c.name
	if check.Failed() {
		check.hint = c.hint
	}
	return check
}

// checkOnce checks the domain once, and then the service, unless the domain does not
// resolve: then the service's check is skipped. It returns both results, in that order.
func (d domainChecks) checkOnce(ctx context.Context) []ServerCheck {
	domain := d.domain.checkOnce(ctx)
	if domain.Failed() {
		return []ServerCheck{domain, {Name: d.service.name, Summary: "skipped, as the domain does not resolve", Skipped: true}}
	}
	return []ServerCheck{domain, d.service.checkOnce(ctx)}
}

// wait makes the check until it passes, for at most c.timeout. It shows each failed
// attempt in the task output, and the check's summary once it passes.
func (c networkCheck) wait(ctx context.Context, output *tui.TaskOutput) error {
	stopped := func() error {
		return clierrors.Wrapf(ctx.Err(), "Stopped waiting for %s", c.waitingFor)
	}
	timeoutAt := time.Now().Add(c.timeout)
	output.SetHeaderLines([]string{fmt.Sprintf("Waiting for %s (timeout: %s)", c.waitingFor, c.timeout)})
	for attemptNdx := 1; ; attemptNdx++ {
		check := c.checkOnce(ctx)
		if ctx.Err() != nil {
			return stopped()
		}
		if !check.Failed() {
			output.AppendLinef("%s: %s", check.Name, check.Summary)
			return nil
		}
		if !time.Now().Before(timeoutAt) {
			return clierrors.Newf("Timed out after %s waiting for %s", c.timeout, c.waitingFor).WithDetails(check.Problems...)
		}
		output.AppendLinef("Attempt %d failed, retrying: %s", attemptNdx, strings.Join(check.Problems, "; "))
		if !pause(ctx, c.interval) {
			return stopped()
		}
	}
}

// CheckServerStatus checks once whether the game server serves clients and the LiveOps
// Dashboard: the checks of WaitForServerToBeReady, without waiting. The checks run
// concurrently, except that a service is checked only once its domain resolves. It
// returns an error only if the checks could not be made, such as when no game server is
// deployed, or were stopped.
func (targetEnv *TargetEnvironment) CheckServerStatus(ctx context.Context) (ServerStatus, error) {
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return ServerStatus{}, err
	}
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return ServerStatus{}, err
	}
	// Unlike the wait, which retries it, fail at once if the GameServer cannot be read.
	fetchCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	gameServer, err := targetEnv.fetchGameServer(fetchCtx, kubeCli)
	if err != nil {
		if errors.Is(err, ErrNoGameServer) {
			return ServerStatus{}, err
		}
		return ServerStatus{}, clierrors.Wrap(err, readGameServerFailure).
			WithSuggestion("Run this command again, and if it fails the same way, check that your account can read the environment's GameServer resource")
	}

	var readiness gameServerReadiness
	var podsErr error
	var notes, failedPodLogs []string
	network := networkChecks(envDetails)
	networkResults := make([][]ServerCheck, len(network))
	var wg sync.WaitGroup
	wg.Go(func() {
		readiness, podsErr = checkGameServerReadiness(ctx, kubeCli, gameServer, gameServer.readinessTarget())
		if readiness.failedPod != "" {
			notes, failedPodLogs = failedPodLogLines(ctx, kubeCli, readiness.failedPod, readiness.failedPodLogSource)
		}
	})
	for ndx, checks := range network {
		wg.Go(func() {
			networkResults[ndx] = checks.checkOnce(ctx)
		})
	}
	wg.Wait()

	if ctx.Err() != nil {
		return ServerStatus{}, clierrors.Wrap(ctx.Err(), "Stopped checking the game server status")
	}
	var networkChecked []ServerCheck
	for _, results := range networkResults {
		networkChecked = append(networkChecked, results...)
	}
	return ServerStatus{
		Checks:        append([]ServerCheck{podsServerCheck(readiness, podsErr)}, networkChecked...),
		FailedPodLogs: failedPodLogs,
		Notes:         notes,
		suggestion:    targetEnv.notReadySuggestion(podsErr, readiness, len(failedPodLogs) > 0, networkChecked),
	}, nil
}

// podsServerCheck is the ServerCheck of one check of the game server pods, given their
// readiness, or the error that stopped the check.
func podsServerCheck(readiness gameServerReadiness, err error) ServerCheck {
	check := ServerCheck{Name: "Game server pods"}
	if err != nil {
		check.Summary = "failed to check"
		check.Problems = []string{fmt.Sprintf("%s: %v", checkPodsFailure, err)}
		return check
	}
	check.Lines = readiness.lines
	check.Problems = readiness.problems
	check.Summary = "ready"
	if !readiness.ready() {
		check.Summary = "not ready"
	}
	return check
}

// notReadySuggestion tells what to do when the game server is not ready, given the
// error that stopped the check of its pods, their readiness, whether the logs of a
// failed pod were fetched, and the network checks. When the pods are ready, it is the
// hint of the first failed network check.
func (targetEnv *TargetEnvironment) notReadySuggestion(podsErr error, readiness gameServerReadiness, hasLogs bool, network []ServerCheck) string {
	switch {
	case podsErr != nil:
		return "The status of the game server pods could not be read from Kubernetes. Run this command again, and if it fails the same way, check that your account can read the environment's pods and stateful sets"
	case readiness.failedPod != "":
		return failedPodSuggestion(targetEnv.HumanID, readiness.failedPod, readiness.failedPodLogSource, hasLogs)
	case !readiness.ready():
		return "If a deploy just started, run this command again in a minute. Otherwise, the pod lines above show what blocks it"
	}
	for _, check := range network {
		if check.hint != "" {
			return check.hint
		}
	}
	return ""
}

// checkDomain checks once that hostname resolves, with lookupDomain.
func checkDomain(ctx context.Context, hostname string) ServerCheck {
	var check ServerCheck
	addrs, err := lookupDomain(ctx, hostname)
	if err != nil {
		check.Summary = "does not resolve"
		check.Problems = []string{fmt.Sprintf("Domain %s does not resolve: %v", hostname, err)}
		return check
	}
	check.Summary = "resolves to " + strings.Join(addrs, ", ")
	return check
}

// checkClientEndpoint connects to the game server's client endpoint clientConnections
// times, and checks that each connection succeeds. The connections are made one after
// another, as each looks up the hostname again, and so may reach another of the load
// balancer's addresses. Concurrent lookups of one hostname share a single answer. Once
// ctx is done, the remaining connections are not made, and do not count as succeeded.
func checkClientEndpoint(ctx context.Context, hostname string, port int) ServerCheck {
	numMade, numFailed := 0, 0
	var firstErr error
	for range clientConnections {
		if ctx.Err() != nil {
			break
		}
		numMade++
		if err := attemptTLSConnection(ctx, hostname, port); err != nil {
			numFailed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	numSucceeded := numMade - numFailed
	check := ServerCheck{Summary: fmt.Sprintf("%d/%d connections succeeded", numSucceeded, clientConnections)}
	var problems []string
	if numFailed > 0 {
		problems = append(problems, fmt.Sprintf("%d of %d connections to %s:%d failed: %v", numFailed, clientConnections, hostname, port, firstErr))
	}
	if numNotMade := clientConnections - numMade; numNotMade > 0 {
		problems = append(problems, fmt.Sprintf("%d of %d connections to %s:%d were not made, as the check ran out of time", numNotMade, clientConnections, hostname, port))
	}
	check.Problems = problems
	return check
}

// checkHTTPServer checks once that url responds with a success or a redirect.
func checkHTTPServer(ctx context.Context, url string) ServerCheck {
	check := ServerCheck{Summary: "does not respond"}
	status, err := probeHTTPServer(ctx, url)
	var certErr *tls.CertificateVerificationError
	switch {
	case status != "":
		check.Summary = "responds with " + status
	case errors.As(err, &certErr):
		check.Summary = "has an invalid certificate"
	}
	if err != nil {
		check.Problems = []string{err.Error()}
	}
	return check
}
