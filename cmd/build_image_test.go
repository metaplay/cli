/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"testing"
)

// TestIsDockerSocketPermissionError checks that only the docker CLI's socket permission error is
// diagnosed as one, and not other "permission denied" text it writes to stderr.
func TestIsDockerSocketPermissionError(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{
			name:   "older docker CLI",
			stderr: "permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: Get \"http://%2Fvar%2Frun%2Fdocker.sock/v1.24/info\": dial unix /var/run/docker.sock: connect: permission denied",
			want:   true,
		},
		{
			name:   "newer docker CLI",
			stderr: "permission denied while trying to connect to the docker API at unix:///var/run/docker.sock",
			want:   true,
		},
		{
			name:   "unreadable config file, daemon stopped",
			stderr: "WARNING: Error loading config file: /home/user/.docker/config.json: open /home/user/.docker/config.json: permission denied\nfailed to connect to the docker API at unix:///var/run/docker.sock; check if the path is correct and if the daemon is running: dial unix /var/run/docker.sock: connect: no such file or directory",
			want:   false,
		},
		{
			name:   "invalid plugin, daemon stopped",
			stderr: "WARNING: Plugin \"/usr/libexec/docker/cli-plugins/docker-scan\" is not valid: failed to fetch metadata: fork/exec /usr/libexec/docker/cli-plugins/docker-scan: permission denied\nCannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New("exit status 1: " + tt.stderr) // As checkCommand formats it
			if got := isDockerSocketPermissionError(err); got != tt.want {
				t.Errorf("isDockerSocketPermissionError = %v, want %v", got, tt.want)
			}
		})
	}
}
