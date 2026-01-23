package util

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// HashConfig generates hash of configuration map
func HashConfig(config map[string]interface{}) (string, error) {
	// Convert config to stable JSON string (sorted keys)
	configJSON, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("failed to marshal config: %w", err)
	}

	hash := sha256.Sum256(configJSON)
	return fmt.Sprintf("%x", hash[:]), nil
}

// HashFile generates hash of a file
func HashFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// HashBytes generates hash of byte slice
func HashBytes(data []byte) string {
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:])
}
