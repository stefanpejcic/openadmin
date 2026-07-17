package paneldb

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestGetCounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			(SELECT COUNT(*) FROM users) AS user_count,
			(SELECT COUNT(*) FROM plans) AS plan_count,
			(SELECT COUNT(*) FROM sites) AS site_count,
			(SELECT COUNT(*) FROM domains) AS domain_count
	`)).WillReturnRows(sqlmock.NewRows([]string{"user_count", "plan_count", "site_count", "domain_count"}).
		AddRow(12, 3, 20, 8))

	counts, err := GetCounts(db)
	if err != nil {
		t.Fatal(err)
	}
	want := Counts{UserCount: 12, PlanCount: 3, SiteCount: 20, DomainCount: 8}
	if counts != want {
		t.Fatalf("expected %+v, got %+v", want, counts)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserContextsExcludesEmptyAndSuspended(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT DISTINCT server FROM users WHERE username NOT LIKE 'SUSPENDED\_%'`)).
		WillReturnRows(sqlmock.NewRows([]string{"server"}).
			AddRow("user1.example.com").
			AddRow(nil).
			AddRow("user2.example.com"))

	contexts, err := UserContexts(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(contexts) != 2 || contexts[0] != "user1.example.com" || contexts[1] != "user2.example.com" {
		t.Fatalf("unexpected contexts: %v", contexts)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDockerContextsAddsOneForRootDefault(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT DISTINCT server FROM users WHERE username NOT LIKE 'SUSPENDED\_%'`)).
		WillReturnRows(sqlmock.NewRows([]string{"server"}).
			AddRow("user1.example.com").
			AddRow("user2.example.com"))

	count, err := DockerContexts(db)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 2 user contexts + 1 default = 3, got %d", count)
	}
}

func TestDockerContextsNoUsersStillCountsDefault(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT DISTINCT server FROM users WHERE username NOT LIKE 'SUSPENDED\_%'`)).
		WillReturnRows(sqlmock.NewRows([]string{"server"}))

	count, err := DockerContexts(db)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected just the default context (1), got %d", count)
	}
}
