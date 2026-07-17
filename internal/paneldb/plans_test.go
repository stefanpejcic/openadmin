package paneldb

import (
	"os"
	"path/filepath"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestGetAllPlansAndUserCountUnrestricted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT\s+plans\.\*`).WillReturnRows(sqlmock.NewRows(
		[]string{"id", "name", "disk_limit", "user_count"}).
		AddRow(1, "Starter", "5000", 3).
		AddRow(2, "Pro", "20000", 1))

	plans, err := GetAllPlansAndUserCount(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[0]["name"] != "Starter" || plans[0]["user_count"] != int64(3) {
		t.Fatalf("unexpected first row: %+v", plans[0])
	}
}

func TestGetAllPlansAndUserCountRestricted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT\s+plans\.\*.*WHERE plans\.id IN \(\?,\?\)`).
		WithArgs(1, 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "user_count"}).AddRow(1, "Starter", 3))

	plans, err := GetAllPlansAndUserCount(db, []int{1, 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0]["id"] != int64(1) {
		t.Fatalf("unexpected result: %+v", plans)
	}
}

func TestGetPlanByID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT \* FROM plans WHERE id = \?`).
		WithArgs("5").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(5, "Pro"))

	plan, err := GetPlanByID(db, "5")
	if err != nil {
		t.Fatal(err)
	}
	if plan["name"] != "Pro" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestGetPlanByIDNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT \* FROM plans WHERE id = \?`).
		WithArgs("999").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))

	if _, err := GetPlanByID(db, "999"); err == nil {
		t.Fatal("expected an error for a missing plan")
	}
}

func TestAllowedPlansForReseller(t *testing.T) {
	dir := t.TempDir()
	origDir := ResellerConfigDir
	ResellerConfigDir = dir
	defer func() { ResellerConfigDir = origDir }()

	if _, ok := AllowedPlansForReseller("nobody"); ok {
		t.Fatal("expected no allowed plans when the reseller file doesn't exist")
	}

	os.WriteFile(filepath.Join(dir, "bob.json"), []byte(`{"allowed_plans": [1, 2, 5]}`), 0644)
	ids, ok := AllowedPlansForReseller("bob")
	if !ok {
		t.Fatal("expected allowed plans to be found for bob")
	}
	if len(ids) != 3 || ids[0] != 1 || ids[2] != 5 {
		t.Fatalf("unexpected allowed plan ids: %v", ids)
	}

	os.WriteFile(filepath.Join(dir, "empty.json"), []byte(`{"allowed_plans": []}`), 0644)
	if _, ok := AllowedPlansForReseller("empty"); ok {
		t.Fatal("expected empty allowed_plans to be treated as 'no plans allowed'")
	}
}
