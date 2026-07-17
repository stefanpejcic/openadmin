package paneldb

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestListSiteNames(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT site_name FROM sites`)).
		WillReturnRows(sqlmock.NewRows([]string{"site_name"}).AddRow("a.com").AddRow("b.com"))

	got, err := ListSiteNames(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a.com" || got[1] != "b.com" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestSearchSiteNames(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT site_name FROM sites WHERE site_name LIKE ? LIMIT 5`)).
		WithArgs("%example%").
		WillReturnRows(sqlmock.NewRows([]string{"site_name"}).AddRow("example.com"))

	got, err := SearchSiteNames(db, "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "example.com" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestListUsernamesUnrestricted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT username FROM users`)).
		WillReturnRows(sqlmock.NewRows([]string{"username"}).AddRow("alice").AddRow("bob"))

	got, err := ListUsernames(db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestListUsernamesRestrictedToReseller(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT username FROM users WHERE owner = ?`)).
		WithArgs("resellerbob").
		WillReturnRows(sqlmock.NewRows([]string{"username"}).AddRow("client1"))

	got, err := ListUsernames(db, "resellerbob")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "client1" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestSearchUsernamesWithResellerScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT username FROM users WHERE username LIKE ? AND owner = ? LIMIT 5`)).
		WithArgs("%ali%", "resellerbob").
		WillReturnRows(sqlmock.NewRows([]string{"username"}).AddRow("alice"))

	got, err := SearchUsernames(db, "ali", "resellerbob")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "alice" {
		t.Fatalf("unexpected result: %+v", got)
	}
}
