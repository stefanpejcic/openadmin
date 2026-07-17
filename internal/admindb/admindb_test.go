package admindb

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func withScratchDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	orig := Path
	Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { Path = orig })

	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesFreshSchema(t *testing.T) {
	db := withScratchDB(t)

	if err := db.CreateUser("admin", "scrypt:32768:8:1$salt$hash", "admin"); err != nil {
		t.Fatal(err)
	}

	u, err := db.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "admin" || !u.IsActive {
		t.Fatalf("unexpected user row: %+v", u)
	}
	if u.TOTPSecret.Valid {
		t.Fatalf("expected null totp_secret on a fresh user, got %+v", u.TOTPSecret)
	}
}

func TestOpenMigratesPreExisting2FAlessDatabase(t *testing.T) {
	dir := t.TempDir()
	orig := Path
	Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { Path = orig })

	// Simulate a users.db created before the totp_secret/totp_enabled
	// columns existed, matching what create_user()'s CREATE TABLE produced
	// on old installs -- Open() must backfill them via ensure_2fa_columns()
	// parity without erroring or dropping existing rows.
	raw, err := sql.Open("sqlite", Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE user (
			id INTEGER PRIMARY KEY,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			is_active BOOLEAN DEFAULT 1 NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO user (username, password_hash, role, is_active) VALUES ('legacy', 'hash', 'admin', 1)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	u, err := db.UserByUsername("legacy")
	if err != nil {
		t.Fatalf("expected pre-existing row to survive migration: %v", err)
	}
	if u.TOTPEnabled {
		t.Fatal("expected totp_enabled to default false after backfill")
	}
}

func TestUserByUsernameNotFound(t *testing.T) {
	db := withScratchDB(t)
	if _, err := db.UserByUsername("nobody"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestWebauthnCredentialRoundTrip(t *testing.T) {
	db := withScratchDB(t)
	if err := db.CreateUser("admin", "hash", "admin"); err != nil {
		t.Fatal(err)
	}
	u, err := db.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.sql.Exec(
		`INSERT INTO webauthn_credential (user_id, credential_id, public_key, sign_count, name) VALUES (?, ?, ?, ?, ?)`,
		u.ID, "cred-123", "pubkey-bytes", 0, "YubiKey",
	); err != nil {
		t.Fatal(err)
	}

	cred, err := db.CredentialByCredentialID("cred-123")
	if err != nil {
		t.Fatal(err)
	}
	if cred.UserID != u.ID || cred.PublicKey != "pubkey-bytes" {
		t.Fatalf("unexpected credential: %+v", cred)
	}

	if err := db.UpdateCredentialSignCount("cred-123", 42); err != nil {
		t.Fatal(err)
	}
	cred, err = db.CredentialByCredentialID("cred-123")
	if err != nil {
		t.Fatal(err)
	}
	if cred.SignCount != 42 {
		t.Fatalf("expected sign_count 42, got %d", cred.SignCount)
	}

	creds, err := db.CredentialsByUserID(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(creds))
	}
}

func TestCreateUserDuplicateUsernameFails(t *testing.T) {
	db := withScratchDB(t)
	if err := db.CreateUser("admin", "hash", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser("admin", "hash2", "admin"); err == nil {
		t.Fatal("expected duplicate username to fail (UNIQUE constraint)")
	}
}
