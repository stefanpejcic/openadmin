package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SecretKeyPath is the on-disk location of the app secret, var not const so tests can point it at a scratch fixture
var SecretKeyPath = "/etc/openpanel/openadmin/secret.key"

// LoadSecretKey reads SecretKeyPath, normally provisioned by the installer, but on a from-source/dev box where it's missing this just generates and persists a new one since it only needs to be unique and stable across restarts
func LoadSecretKey() (string, error) {
	raw, err := os.ReadFile(SecretKeyPath)
	if err == nil {
		return strings.TrimSpace(string(raw)), nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read secret key file %s: %w", SecretKeyPath, err)
	}
	return GenerateAndPersistSecret(SecretKeyPath)
}

// GenerateAndPersistSecret creates a random hex-encoded secret and writes it to path with owner-only permissions, so subsequent startups reuse it instead of minting a fresh one
func GenerateAndPersistSecret(path string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate a new secret for %s: %w", path, err)
	}
	secret := hex.EncodeToString(b)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("failed to create directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		return "", fmt.Errorf("failed to persist new secret to %s: %w", path, err)
	}
	return secret, nil
}

// deriveKey turns the raw secret into a fixed-size key per purpose (session signing vs CSRF) so they don't share key material despite coming from the same on-disk secret
func deriveKey(secret, purpose string) []byte {
	sum := sha256.Sum256([]byte(purpose + ":" + secret))
	return sum[:]
}
