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

// SecretKeyPath is the on-disk location of the app secret. Var (not const)
// so tests can point it at a scratch fixture.
var SecretKeyPath = "/etc/openpanel/openadmin/secret.key"

// LoadSecretKey reads SecretKeyPath. Normally the file is provisioned by
// the installer before the app ever runs, but for a from-source build/dev
// box (or a fresh install run out of order) there's no reason to hard-fail
// if it's missing: the secret only needs to be unique per server and
// stable across restarts, so this generates and persists a new one on the
// spot rather than requiring a manual provisioning step first.
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

// GenerateAndPersistSecret creates a new random, hex-encoded secret and
// writes it to path (creating parent directories as needed) with
// owner-only permissions, so the same value is reused on subsequent
// startups instead of a fresh one being minted every time.
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

// deriveKey turns the raw secret file content into a fixed-size key for a
// given purpose (session signing vs. CSRF), so the two primitives don't
// share key material even though they're derived from the same on-disk
// secret. The derived key only needs to be stable across restarts of this
// process, which hashing the same on-disk secret gives us.
func deriveKey(secret, purpose string) []byte {
	sum := sha256.Sum256([]byte(purpose + ":" + secret))
	return sum[:]
}
