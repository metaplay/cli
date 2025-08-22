/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package kubeutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// createDebugContainerName generates a unique debug container name with a random hex string.
func createDebugContainerName() (string, error) {
	// Generate a random 8-byte array
	randomBytes := make([]byte, 8)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Convert the bytes to a hex string
	hexString := hex.EncodeToString(randomBytes)

	// Create the debug container name
	return fmt.Sprintf("debugger-%s", hexString), nil
}
