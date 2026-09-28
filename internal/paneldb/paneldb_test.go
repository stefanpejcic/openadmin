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
