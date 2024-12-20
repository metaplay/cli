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

// resolveCredentialsFilePath resolves the file path for storing the encrypted data.
// It follows platform-specific best practices for Linux, macOS, and Windows.
func resolveCredentialsFilePath() (string, error) {
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
		// Linux and other Unix-like systems: Use ~/.config for user-specific configuration data
		baseDir = filepath.Join(homeDir, ".config", "metaplay")
	}

	// Ensure the directory exists
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create directory for file path: %w", err)
	}

	// Return the resolved file path
	return filepath.Join(baseDir, "credentials"), nil
}

// SaveTokenSet encrypts and persists a TokenSet to a file.
func SaveTokenSet(tokenSet *TokenSet) error {
	key, err := getOrCreateAESKey()
	if err != nil {
		return err
	}

	// Serialize the TokenSet to JSON
	data, err := json.Marshal(tokenSet)
	if err != nil {
		return fmt.Errorf("failed to serialize TokenSet: %w", err)
	}

	// Encrypt the data
	encryptedData, err := encrypt(data, key)
	if err != nil {
		return fmt.Errorf("failed to encrypt TokenSet: %w", err)
	}

	// Resolve file path
	filePath, err := resolveCredentialsFilePath()
	if err != nil {
		return err
	}

	// Save encrypted data to the resolved file path
	err = os.WriteFile(filePath, encryptedData, 0600)
	if err != nil {
		return fmt.Errorf("failed to write encrypted TokenSet to file: %w", err)
	}

	return nil
}

// LoadTokenSet decrypts and retrieves a TokenSet from a file.
func LoadTokenSet() (*TokenSet, error) {
	key, err := getOrCreateAESKey()
	if err != nil {
		return nil, err
	}

	// Resolve file path
	filePath, err := resolveCredentialsFilePath()
	if err != nil {
		return nil, err
	}

	// Read the encrypted data from the resolved file path
	encryptedData, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // TokenSet does not exist
		}
		return nil, fmt.Errorf("failed to read encrypted TokenSet from file: %w", err)
	}

	// Decrypt the data
	data, err := decrypt(encryptedData, key)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt TokenSet: %w", err)
	}

	// Deserialize the JSON into a TokenSet
	var tokenSet TokenSet
	err = json.Unmarshal(data, &tokenSet)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize TokenSet: %w", err)
	}

	return &tokenSet, nil
}

// DeleteTokenSet removes the encrypted TokenSet file.
func DeleteTokenSet() error {
	filePath, err := resolveCredentialsFilePath()
	if err != nil {
		return err
	}

	err = os.Remove(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No file to delete
		}
		return fmt.Errorf("failed to delete TokenSet file: %w", err)
	}
	return nil
}
