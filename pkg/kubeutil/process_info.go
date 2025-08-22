/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
)

// Information about the running server process.
type ServerProcessInfo struct {
	Pid      int     // Pid of the server process.
	Username string  // Username running the server process.
	MemoryGB float64 // Heap size of server process in gigabytes.
}

// Helper function to get process information (PID, username, and memory usage) from
// a game server running in a pod.
func GetServerProcessInformation(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string) (*ServerProcessInfo, error) {
	// Get PID using kubectl exec in the debug container
	log.Debug().Msgf("Get game server process information...")
	stdout, _, err := ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		`pgrep -f Server`,
	)
	if err != nil {
		log.Error().Msgf("Failed to get game server process information: %v", err) // Removed stderr from log, it's already piped
		return nil, err
	}

	// Parse and validate PID from stdout
	pidStr := strings.TrimSpace(stdout)
	if pidStr == "" {
		return nil, fmt.Errorf("unable to resolve game server PID: got empty response")
	}

	// Parse the PID.
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return nil, fmt.Errorf("invalid PID format '%s', must be a positive integer", pidStr)
	}
	log.Debug().Msgf("Found game server process with PID: %s", pidStr)

	// Get username running the Server process.
	log.Debug().Msgf("Get user which is running the game server process...")
	stdout, _, err = ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		fmt.Sprintf("ps -o user= -p %s", pidStr),
	)
	if err != nil {
		log.Error().Msgf("Failed to get game server process information: %v", err) // Removed stderr from log, it's already piped
		return nil, err
	}

	// Parse and validate username from stdout.
	username := strings.TrimSpace(stdout)
	if username == "" {
		return nil, fmt.Errorf("unable to resolve user: got empty response")
	}

	// Validate that the username follows Unix/Linux username conventions
	if err := validateUnixUsername(username); err != nil {
		return nil, fmt.Errorf("invalid username '%s': %v", username, err)
	}
	log.Debug().Msgf("Game server is running as user: %s", username)

	// Get process memory information using standard Linux tools
	log.Debug().Msgf("Getting process memory information...")
	stdout, _, err = ExecInDebugContainer(ctx, kubeCli, podName, debugContainerName,
		fmt.Sprintf("cat /proc/%s/status", pidStr),
	)
	if err != nil {
		log.Error().Msgf("Failed to get game server memory information: %v", err) // Removed stderr
		return nil, err
	}

	// log.Debug().Msgf("Full /proc/%s/status:\n%s", pid, stdout)

	// Parse memory information from /proc/[pid]/status
	// Format example:
	// VmSize:   12345678 kB
	// VmRSS:     1234567 kB
	var vmRss int64
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] != "kB" {
			continue
		}

		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}

		if fields[0] == "VmRSS:" {
			vmRss = value
		}
	}

	if vmRss == 0 {
		return nil, fmt.Errorf("failed to parse memory information from /proc/status")
	}

	// Convert from kB to GB
	memoryGB := float64(vmRss) / (1024 * 1024.0)
	return &ServerProcessInfo{
		Pid:      pid,
		Username: username,
		MemoryGB: memoryGB,
	}, nil
}

// validateUnixUsername checks if a username follows standard Unix/Linux username conventions:
// - Only contains alphanumeric characters, underscores, and hyphens
// - Starts with a letter or underscore
// - Length between 1 and 32 characters
func validateUnixUsername(username string) error {
	if len(username) == 0 {
		return fmt.Errorf("username cannot be empty")
	}
	if len(username) > 32 {
		return fmt.Errorf("username cannot be longer than 32 characters")
	}

	// Check first character
	first := username[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '_') {
		return fmt.Errorf("username must start with a letter or underscore")
	}

	// Check remaining characters
	for i := 1; i < len(username); i++ {
		c := username[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return fmt.Errorf("username can only contain letters, numbers, underscores, and hyphens")
		}
	}

	return nil
}
