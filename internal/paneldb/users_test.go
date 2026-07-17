package paneldb

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestCheckIfOwnerForUserNonResellerAlwaysTrue(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// no ExpectQuery registered -- a non-reseller role must not even query
	// the database, since CheckIfOwnerForUser returns true unconditionally

	if !CheckIfOwnerForUser(db, "someuser", "admin", "admin") {
		t.Fatal("expected admin role to always be treated as owner")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckIfOwnerForUserResellerOwnsAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT 1 FROM users WHERE username = ? AND owner = ? LIMIT 1`)).
		WithArgs("client1", "resellerbob").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	if !CheckIfOwnerForUser(db, "client1", "resellerbob", "reseller") {
		t.Fatal("expected reseller to own the account")
	}
}

func TestCheckIfOwnerForUserResellerDoesNotOwnAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT 1 FROM users WHERE username = ? AND owner = ? LIMIT 1`)).
		WithArgs("someoneelse", "resellerbob").
		WillReturnRows(sqlmock.NewRows([]string{"1"}))

	if CheckIfOwnerForUser(db, "someoneelse", "resellerbob", "reseller") {
		t.Fatal("expected reseller to not own an account they don't own")
	}
}

func TestGetAllUsersUnrestricted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT users\.\*`).WillReturnRows(sqlmock.NewRows(
		[]string{"id", "username", "name", "disk_limit"}).AddRow(1, "alice", "Starter", "5000"))

	users, err := GetAllUsers(db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0]["username"] != "alice" {
		t.Fatalf("unexpected result: %+v", users)
	}
}

func TestGetAllUsersRestrictedToReseller(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT users\.\*.*WHERE users\.owner = \?`).
		WithArgs("bob").
		WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(2, "aliceclient"))

	users, err := GetAllUsers(db, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
}

func TestGetUserDataByUsernameMatchesSuspendedPattern(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT u.username, u.id, u.email, u.owner, u.twofa_enabled, u.registered_date, u.plan_id, u.server`)).
		WithArgs("alice", "SUSPENDED_%_alice").
		WillReturnRows(sqlmock.NewRows([]string{"username", "id", "email", "owner", "twofa_enabled", "registered_date", "plan_id", "server"}).
			AddRow("SUSPENDED_1730000000_alice", 5, "alice@example.com", nil, false, "2025-01-01", 2, "alice"))

	data, err := GetUserDataByUsername(db, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if data.Username != "SUSPENDED_1730000000_alice" || data.Context != "alice" {
		t.Fatalf("unexpected data: %+v", data)
	}
}

func TestGetUserDataByUsernameNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT u.username, u.id, u.email, u.owner, u.twofa_enabled, u.registered_date, u.plan_id, u.server`)).
		WithArgs("ghost", "SUSPENDED_%_ghost").
		WillReturnRows(sqlmock.NewRows([]string{"username", "id", "email", "owner", "twofa_enabled", "registered_date", "plan_id", "server"}))

	if _, err := GetUserDataByUsername(db, "ghost"); err == nil {
		t.Fatal("expected an error for a missing user")
	}
}
