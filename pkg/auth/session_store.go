/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/rs/zerolog/log"
	"github.com/zalando/go-keyring"
)

// Service name and keyring key
const (
	keyringService = "metaplay-cli"
	keyringKey     = "encryption-key"
)

// Hard-coded encryption key for Linux as it doesn't have a reliable keyring.
// We rely on the filesystem access control to protect the secrets.
var hardCodedKey = []byte{7, 246, 197, 129, 44, 88, 77, 229, 221, 48, 42, 6, 54, 141, 173, 238, 162, 83, 31, 12, 241, 254, 170, 86, 247, 233, 103, 130, 205, 243, 36, 61}

// Type of user in portal (human or machine).
type UserType string

const (
	UserTypeHuman   UserType = "human"
	UserTypeMachine          = "machine"
)

// In-memory session state.
type SessionState struct {
	UserType UserType  // Type of user in portal.
	TokenSet *TokenSet // TokenSet for the user.
}

// Persisted session state (with encrypted tokenSet).
type PersistedSessionState struct {
	UserType        UserType `json:"userType"` // Type of the user (human or machine)
	EncodedTokenSet string   `json:"tokenSet"` // Encrypted tokenSet
}

// Represents the config.json persisted on disk.
type PersistedConfig struct {
	Sessions map[string]PersistedSessionState `json:"sessions"` // Persisted sessions, use sessionID as key.
}

func newPersistedConfig() *PersistedConfig {
	return &PersistedConfig{
		Sessions: make(map[string]PersistedSessionState),
	}
}

// Generate or retrieve the AES encryption key from the keyring.
func getOrCreateAESKey() ([]byte, error) {
	// On Linux, there is no reliably keyring available, so we resort to a fixed key.
	if runtime.GOOS == "linux" {
		return hardCodedKey, nil
	}

	// Get the AES key from the OS keyring.
	key, err := keyring.Get(keyringService, keyringKey)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			// Generate a new AES key
			log.Debug().Msg("Generate new AES encryption key")
			newKey := make([]byte, 32) // AES-256
			if _, err := rand.Read(newKey); err != nil {
				return nil, fmt.Errorf("failed to generate AES key: %w", err)
			}

			// Store the key in the keyring
			log.Debug().Msg("Store encryption key in keyring")
			err = keyring.Set(keyringService, keyringKey, base64.StdEncoding.EncodeToString(newKey))
			if err != nil {
				return nil, fmt.Errorf("failed to save AES key to keyring: %w", err)
			}
			return newKey, nil
		}
		return nil, fmt.Errorf("failed to retrieve AES key: %w", err)
	}

	// Decode the stored key
	decodedKey, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("failed to decode AES key: %w", err)
	}
	return decodedKey, nil
}

// Encrypt data using AES encryption.
func encrypt(data []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	ciphertext := make([]byte, aes.BlockSize+len(data))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("failed to generate IV: %w", err)
	}

	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(ciphertext[aes.BlockSize:], data)

	return ciphertext, nil
}

// Decrypt data using AES decryption.
func decrypt(data []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	iv := data[:aes.BlockSize]
	data = data[aes.BlockSize:]

	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(data, data)

	return data, nil
}

// resolvePersistedConfigFilePath resolves the path to the persisted configuration.
// It follows platform-specific best practices for Linux, macOS, and Windows.
func resolvePersistedConfigFilePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user's home directory: %w", err)
	}

	// Use the appropriate directory for storing application data
	var baseDir string
	if runtime.GOOS == "windows" {
		// Windows: Use AppData\Local for application-specific data
		baseDir = filepath.Join(homeDir, "AppData", "Local", "Metaplay")
	} else if runtime.GOOS == "darwin" {
		// macOS: Use ~/Library/Application Support for application data
		baseDir = filepath.Join(homeDir, "Library", "Application Support", "Metaplay")
	} else {
		// Linux and other Unix-like systems: Use ~/.config/metaplay for user-specific configuration data
		baseDir = filepath.Join(homeDir, ".config", "metaplay")
	}

	// Ensure the directory exists
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create directory for file path: %w", err)
	}

	// Return the resolved file path
	return filepath.Join(baseDir, "config.json"), nil
}

// Load the persisted config file on disk. Returns an empty default state if the
// file does not exist.
func loadPersistedConfig() (*PersistedConfig, error) {
	// Resolve path to the file.
	filePath, err := resolvePersistedConfigFilePath()
	if err != nil {
		return nil, err
	}

	// Read persisted config JSON from file
	persistedConfigJSON, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create a new persisted config if file does not exist.
			return newPersistedConfig(), nil
		}
		return nil, err
	}

	// Deserialize config.
	var persistedConfig PersistedConfig
	err = json.Unmarshal(persistedConfigJSON, &persistedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal session state JSON: %w", err)
	}

	return &persistedConfig, nil
}

// Save the persisted config back to the file on disk.
func savePersistedConfig(config *PersistedConfig) error {
	// Resolve path to the file.
	filePath, err := resolvePersistedConfigFilePath()
	if err != nil {
		return err
	}

	// Serialize the sessionState to JSON
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to serialize PersistedConfig: %w", err)
	}

	// Write sessionState to file.
	err = os.WriteFile(filePath, configJSON, 0600)
	if err != nil {
		return fmt.Errorf("failed to write session sate to file: %w", err)
	}

	return nil
}

// Load the persisted config from disk, apply the update, and then persist the config back to disk.
func updatePersistedConfig(updateFunc func(*PersistedConfig) error) error {
	// Load config from disk.
	configState, err := loadPersistedConfig()
	if err != nil {
		return err
	}

	// Apply the user-provided update.
	err = updateFunc(configState)
	if err != nil {
		return err
	}

	// Persist back to disk.
	return savePersistedConfig(configState)
}

// SaveSessionState saves the current session state (with encrypted tokenSet).
func SaveSessionState(sessionID string, userType UserType, tokenSet *TokenSet) error {
	// Serialize the tokenSet to JSON
	tokenSetJSON, err := json.Marshal(tokenSet)
	if err != nil {
		return fmt.Errorf("failed to serialize TokenSet: %w", err)
	}

	// Get an encryption key.
	key, err := getOrCreateAESKey()
	if err != nil {
		return err
	}

	// Encrypt the tokenSet
	encryptedTokenSet, err := encrypt(tokenSetJSON, key)
	if err != nil {
		return fmt.Errorf("failed to encrypt TokenSet: %w", err)
	}

	// Construct session state.
	sessionState := PersistedSessionState{
		UserType:        userType,
		EncodedTokenSet: base64.StdEncoding.EncodeToString(encryptedTokenSet),
	}

	// Update session state in persisted config.
	updatePersistedConfig(func(config *PersistedConfig) error {
		config.Sessions[sessionID] = sessionState
		return nil
	})

	return nil
}

// LoadSessionState loads a session state and decrypts the tokenSet.
// Returns nil if there is no existing session.
func LoadSessionState(sessionID string) (*SessionState, error) {
	// Load persisted config
	persistedConfig, err := loadPersistedConfig()
	if err != nil {
		return nil, err
	}

	// Get session state.
	sessionState, found := persistedConfig.Sessions[sessionID]
	if !found {
		// Session not found, return nil (but no error).
		return nil, nil
	}

	// Base64 decode to get encrypted tokenSet bytes.
	tokenSetBytes, err := base64.StdEncoding.DecodeString(sessionState.EncodedTokenSet)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted tokenSet: %w", err)
	}

	// Get encryption key.
	key, err := getOrCreateAESKey()
	if err != nil {
		return nil, err
	}

	// Decrypt the tokenSet
	tokenSetJSON, err := decrypt(tokenSetBytes, key)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt TokenSet: %w", err)
	}

	// Deserialize the JSON into a TokenSet.
	var tokenSet TokenSet
	err = json.Unmarshal(tokenSetJSON, &tokenSet)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize TokenSet: %w", err)
	}

	return &SessionState{
		UserType: sessionState.UserType,
		TokenSet: &tokenSet,
	}, nil
}

// DeleteSessionState removes the current session state (i.e., signs out the user).
func DeleteSessionState(sessionID string) error {
	// Remove the session from the persisted config.
	return updatePersistedConfig(func(config *PersistedConfig) error {
		delete(config.Sessions, sessionID)
		return nil
	})
}
