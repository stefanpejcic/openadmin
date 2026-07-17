package paneldb

import (
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestGetAllDomains(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(1, "/var/www/html", "example.com", "8.2", "alice"))

	domains, err := GetAllDomains(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 1 || domains[0]["domain_url"] != "example.com" {
		t.Fatalf("unexpected result: %+v", domains)
	}
}
