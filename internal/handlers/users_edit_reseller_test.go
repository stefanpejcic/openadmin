package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/paneldb"
)

func newResellerReassignTest(t *testing.T, limits string) (*Users, sqlmock.Sqlmock, string) {
	t.Helper()
	dir := t.TempDir()
	origDir, origTpl := paneldb.ResellerConfigDir, paneldb.ResellerTemplatePath
	paneldb.ResellerConfigDir = filepath.Join(dir, "resellers")
	paneldb.ResellerTemplatePath = filepath.Join(dir, "reseller_template.json")
	t.Cleanup(func() { paneldb.ResellerConfigDir, paneldb.ResellerTemplatePath = origDir, origTpl })
	os.WriteFile(paneldb.ResellerTemplatePath, []byte(`{"max_accounts":0,"current_accounts":0,"allowed_plans":[],"max_disk_blocks":0,"current_disk_blocks":0}`), 0644)
	if limits != "" {
		os.MkdirAll(paneldb.ResellerConfigDir, 0755)
		os.WriteFile(filepath.Join(paneldb.ResellerConfigDir, "res1.json"), []byte(limits), 0644)
	}

	adminDB := newAPITestDB(t)
	if err := adminDB.CreateUser("res1", "x", "reseller"); err != nil {
		t.Fatal(err)
	}
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mysqlDB.Close() })
	return &Users{MySQL: mysqlDB, AdminDB: adminDB}, mock, filepath.Join(paneldb.ResellerConfigDir, "res1.json")
}

func expectReassignQueries(mock sqlmock.Sqlmock, count, planID int) {
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM users WHERE owner = \?`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(count))
	mock.ExpectQuery(`SELECT plan_id FROM users WHERE username = \?`).WillReturnRows(sqlmock.NewRows([]string{"plan_id"}).AddRow(planID))
}

func TestUpdateUserResellerCreatesMissingLimitsFile(t *testing.T) {
	u, mock, path := newResellerReassignTest(t, "")
	expectReassignQueries(mock, 0, 1)

	ok, msg := u.updateUserReseller("bob", "res1")
	if ok {
		t.Fatal("expected refusal, the template has no allowed plans")
	}
	if !strings.Contains(msg, "not assigned") {
		t.Fatalf("unexpected message: %q", msg)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected limits file to be created from the template: %v", err)
	}
}

func TestUpdateUserResellerRefusesPlanNotAllowed(t *testing.T) {
	u, mock, _ := newResellerReassignTest(t, `{"allowed_plans":["2","3"]}`)
	expectReassignQueries(mock, 0, 1)

	if ok, msg := u.updateUserReseller("bob", "res1"); ok || !strings.Contains(msg, "not assigned") {
		t.Fatalf("expected plan refusal, got ok=%v msg=%q", ok, msg)
	}
}

func TestUpdateUserResellerRefusesOverDisk(t *testing.T) {
	u, mock, _ := newResellerReassignTest(t, `{"allowed_plans":[1],"max_disk_blocks":100,"current_disk_blocks":100}`)
	expectReassignQueries(mock, 0, 1)

	if ok, msg := u.updateUserReseller("bob", "res1"); ok || !strings.Contains(msg, "disk") {
		t.Fatalf("expected disk refusal, got ok=%v msg=%q", ok, msg)
	}
}

func TestUpdateUserResellerAllowsWithinLimits(t *testing.T) {
	u, mock, _ := newResellerReassignTest(t, `{"allowed_plans":["1"],"max_accounts":5,"max_disk_blocks":100,"current_disk_blocks":10}`)
	expectReassignQueries(mock, 2, 1)
	mock.ExpectExec(`UPDATE users SET owner = \? WHERE username = \?`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM users WHERE owner = \?`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(3))

	if ok, msg := u.updateUserReseller("bob", "res1"); !ok {
		t.Fatalf("expected success, got %q", msg)
	}
}
