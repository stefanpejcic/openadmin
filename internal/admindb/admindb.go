// Package admindb wraps OpenAdmin's SQLite users.db: the "user" and
// "webauthn_credential" tables backing admin login, 2FA, and passkey
// credentials.
//
// The schema includes the totp_secret/totp_enabled columns and the
// webauthn_credential table, both backfilled onto existing databases at
// startup by migrate() below.
package admindb

import (
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

// Path is a var (not const) so tests can point it at a scratch fixture
// instead of the real /etc path.
var Path = "/etc/openpanel/openadmin/users.db"

var ErrNotFound = errors.New("admindb: not found")

type DB struct {
	sql *sql.DB
}

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	IsActive     bool
	TOTPSecret   sql.NullString
	TOTPEnabled  bool
}

type WebauthnCredential struct {
	ID           int64
	UserID       int64
	CredentialID string
	PublicKey    string
	SignCount    uint32
	Name         sql.NullString
	CreatedAt    time.Time
}

// Open opens Path and ensures the schema exists, running idempotent
// migrations against an existing database if needed.
func Open() (*DB, error) {
	sqlDB, err := sql.Open("sqlite", Path)
	if err != nil {
		return nil, err
	}
	// SQLite only safely supports one writer at a time; database/sql's
	// pool would otherwise open concurrent connections that each think
	// they hold the write lock.
	sqlDB.SetMaxOpenConns(1)

	db := &DB{sql: sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error { return db.sql.Close() }

func (db *DB) migrate() error {
	if _, err := db.sql.Exec(`
		CREATE TABLE IF NOT EXISTS user (
			id INTEGER PRIMARY KEY,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			is_active BOOLEAN DEFAULT 1 NOT NULL
		)
	`); err != nil {
		return err
	}

	columns, err := db.userColumns()
	if err != nil {
		return err
	}
	if !columns["totp_secret"] {
		if _, err := db.sql.Exec(`ALTER TABLE user ADD COLUMN totp_secret TEXT`); err != nil {
			return err
		}
	}
	if !columns["totp_enabled"] {
		if _, err := db.sql.Exec(`ALTER TABLE user ADD COLUMN totp_enabled BOOLEAN DEFAULT 0 NOT NULL`); err != nil {
			return err
		}
	}

	if _, err := db.sql.Exec(`
		CREATE TABLE IF NOT EXISTS webauthn_credential (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			credential_id TEXT UNIQUE NOT NULL,
			public_key TEXT NOT NULL,
			sign_count INTEGER DEFAULT 0 NOT NULL,
			name TEXT,
			created_at TEXT
		)
	`); err != nil {
		return err
	}

	return nil
}

func (db *DB) userColumns() (map[string]bool, error) {
	rows, err := db.sql.Query(`PRAGMA table_info(user)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid         int
			name, ctype string
			notNull     int
			dfltValue   sql.NullString
			pk          int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func (db *DB) UserByUsername(username string) (*User, error) {
	return db.scanUser(db.sql.QueryRow(
		`SELECT id, username, password_hash, role, is_active, totp_secret, totp_enabled FROM user WHERE username = ?`,
		username,
	))
}

func (db *DB) UserByID(id int64) (*User, error) {
	return db.scanUser(db.sql.QueryRow(
		`SELECT id, username, password_hash, role, is_active, totp_secret, totp_enabled FROM user WHERE id = ?`,
		id,
	))
}

func (db *DB) scanUser(row *sql.Row) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.IsActive, &u.TOTPSecret, &u.TOTPEnabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (db *DB) CredentialByCredentialID(credentialID string) (*WebauthnCredential, error) {
	var c WebauthnCredential
	var createdAt sql.NullString
	err := db.sql.QueryRow(
		`SELECT id, user_id, credential_id, public_key, sign_count, name, created_at FROM webauthn_credential WHERE credential_id = ?`,
		credentialID,
	).Scan(&c.ID, &c.UserID, &c.CredentialID, &c.PublicKey, &c.SignCount, &c.Name, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if createdAt.Valid {
		if t, err := time.Parse(time.RFC3339, createdAt.String); err == nil {
			c.CreatedAt = t
		}
	}
	return &c, nil
}

// CredentialsByUserID lists all passkeys registered to a user (for the
// passkeys management page and for building WebAuthn allow-credential
// lists).
func (db *DB) CredentialsByUserID(userID int64) ([]WebauthnCredential, error) {
	rows, err := db.sql.Query(
		`SELECT id, user_id, credential_id, public_key, sign_count, name, created_at FROM webauthn_credential WHERE user_id = ?`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WebauthnCredential
	for rows.Next() {
		var c WebauthnCredential
		var createdAt sql.NullString
		if err := rows.Scan(&c.ID, &c.UserID, &c.CredentialID, &c.PublicKey, &c.SignCount, &c.Name, &createdAt); err != nil {
			return nil, err
		}
		if createdAt.Valid {
			if t, err := time.Parse(time.RFC3339, createdAt.String); err == nil {
				c.CreatedAt = t
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (db *DB) UpdateCredentialSignCount(credentialID string, signCount uint32) error {
	_, err := db.sql.Exec(`UPDATE webauthn_credential SET sign_count = ? WHERE credential_id = ?`, signCount, credentialID)
	return err
}

// CreateUser inserts a new user row. password must already be hashed (see
// auth.GeneratePasswordHash). If the username already exists, this returns
// a plain error rather than ErrNotFound -- a duplicate username is a
// distinct condition from "not found".
func (db *DB) CreateUser(username, passwordHash, role string) error {
	_, err := db.sql.Exec(
		`INSERT INTO user (username, password_hash, role, is_active) VALUES (?, ?, ?, 1)`,
		username, passwordHash, role,
	)
	return err
}

// SetActive backs the admin-suspend/reactivate action available from the
// administrators list.
func (db *DB) SetActive(username string, active bool) error {
	_, err := db.sql.Exec(`UPDATE user SET is_active = ? WHERE username = ?`, active, username)
	return err
}

// SetTOTP enables or disables 2FA for a user.
func (db *DB) SetTOTP(username, secret string, enabled bool) error {
	_, err := db.sql.Exec(`UPDATE user SET totp_secret = ?, totp_enabled = ? WHERE username = ?`, secret, enabled, username)
	return err
}

func (db *DB) AllUsers() ([]User, error) {
	rows, err := db.sql.Query(`SELECT id, username, password_hash, role, is_active, totp_secret, totp_enabled FROM user`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.IsActive, &u.TOTPSecret, &u.TOTPEnabled); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdatePasswordHash updates a user's stored password hash. The caller is
// responsible for any role-based permission checks first -- see
// handlers.Administrators.
func (db *DB) UpdatePasswordHash(username, passwordHash string) error {
	_, err := db.sql.Exec(`UPDATE user SET password_hash = ? WHERE username = ?`, passwordHash, username)
	return err
}

// RenameUser backs the "rename_user" admin action.
func (db *DB) RenameUser(oldUsername, newUsername string) error {
	_, err := db.sql.Exec(`UPDATE user SET username = ? WHERE username = ?`, newUsername, oldUsername)
	return err
}

// DeleteUser backs the "delete" admin action.
func (db *DB) DeleteUser(username string) error {
	_, err := db.sql.Exec(`DELETE FROM user WHERE username = ?`, username)
	return err
}

func (db *DB) CredentialCountByUserID(userID int64) (int, error) {
	var count int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM webauthn_credential WHERE user_id = ?`, userID).Scan(&count)
	return count, err
}

func (db *DB) DeleteCredentialsByUserID(userID int64) error {
	_, err := db.sql.Exec(`DELETE FROM webauthn_credential WHERE user_id = ?`, userID)
	return err
}

// CreateCredential inserts a new WebauthnCredential row for a registered
// passkey.
func (db *DB) CreateCredential(userID int64, credentialID, publicKey string, signCount uint32, name string) (int64, error) {
	res, err := db.sql.Exec(
		`INSERT INTO webauthn_credential (user_id, credential_id, public_key, sign_count, name, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, credentialID, publicKey, signCount, name, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CredentialBelongsToUser reports whether the credential with the given id
// belongs to the given user.
func (db *DB) CredentialBelongsToUser(credentialID, userID int64) bool {
	var count int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM webauthn_credential WHERE id = ? AND user_id = ?`, credentialID, userID).Scan(&count)
	return err == nil && count > 0
}

// RenameCredential updates a credential's display name.
func (db *DB) RenameCredential(id int64, name string) error {
	_, err := db.sql.Exec(`UPDATE webauthn_credential SET name = ? WHERE id = ?`, name, id)
	return err
}

// DeleteCredentialByID deletes a single Webauthn credential by its row id.
func (db *DB) DeleteCredentialByID(id int64) error {
	_, err := db.sql.Exec(`DELETE FROM webauthn_credential WHERE id = ?`, id)
	return err
}
