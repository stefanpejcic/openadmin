package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSecretKeyReadsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")
	os.WriteFile(path, []byte("existing-secret\n"), 0600)

	orig := SecretKeyPath
	SecretKeyPath = path
	t.Cleanup(func() { SecretKeyPath = orig })

	got, err := LoadSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	if got != "existing-secret" {
		t.Fatalf("expected the existing (trimmed) secret returned, got %q", got)
	}
}

func TestLoadSecretKeyGeneratesAndPersistsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	// A nested, not-yet-existing directory, to also exercise the
	// parent-directory creation.
	path := filepath.Join(dir, "nested", "secret.key")

	orig := SecretKeyPath
	SecretKeyPath = path
	t.Cleanup(func() { SecretKeyPath = orig })

	got, err := LoadSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("expected a non-empty generated secret")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the generated secret persisted to disk: %v", err)
	}
	if strings.TrimSpace(string(raw)) != got {
		t.Fatalf("expected the persisted file to match the returned secret, got %q vs %q", string(raw), got)
	}

	// A second load must reuse the same persisted value, not mint a new one.
	got2, err := LoadSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	if got2 != got {
		t.Fatalf("expected the second load to reuse the persisted secret, got %q vs %q", got2, got)
	}
}

func TestGenerateAndPersistSecretProducesDistinctValues(t *testing.T) {
	dir := t.TempDir()
	a, err := GenerateAndPersistSecret(filepath.Join(dir, "a.key"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateAndPersistSecret(filepath.Join(dir, "b.key"))
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("expected two independently generated secrets to differ")
	}
	if len(a) == 0 || len(b) == 0 {
		t.Fatal("expected non-empty generated secrets")
	}
}
