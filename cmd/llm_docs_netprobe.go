/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Returns true if the network access is likely denied to the target host.
func isNetworkAccessDenied(target string) bool {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return false
	}

	// A direct probe cannot diagnose a connection made through a proxy.
	if proxyURL, err := http.ProxyFromEnvironment(&http.Request{URL: &url.URL{Scheme: "https", Host: target}}); err != nil || proxyURL != nil {
		return false
	}

	// Limit the delay added by diagnostics.
	const probeTimeout = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return false
	}

	// Test all resolved addresses and require each attempt to be denied.
	// Ignore "network unreachable" errors, for example when IPv6 is unavailable.
	var dialer net.Dialer
	accessDenied := false
	for _, addr := range addrs {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(addr, port))
		if err == nil {
			_ = conn.Close()
			return false
		}
		if isSocketPermissionError(err) {
			accessDenied = true
			continue
		}
		if !isSocketNetworkUnreachableError(err) {
			return false
		}
	}
	return accessDenied
}
