package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/auth"
)

func withScratchUserHomeBase(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := UserHomeBase
	UserHomeBase = dir
	t.Cleanup(func() { UserHomeBase = orig })
	return dir
}

func expectExclusionUsers(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT username, server FROM users ORDER BY username`).
		WillReturnRows(sqlmock.NewRows([]string{"username", "server"}).
			AddRow("alice", "alice").
			AddRow("bob", "bob-ctx").
			AddRow("carol", nil).
			AddRow("SUSPENDED_123_dave", "dave").
			AddRow("evil", "../etc"))
}

func TestBackupsListExclusionsReadsMarkers(t *testing.T) {
	home := withScratchUserHomeBase(t)
	for _, d := range []string{"alice", "bob-ctx", "carol"} {
		os.MkdirAll(filepath.Join(home, d), 0755)
	}
	os.WriteFile(filepath.Join(home, "bob-ctx", "backup.off"), nil, 0644)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectExclusionUsers(mock)

	rows, err := backupsListExclusions(db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]backupExcludeRow{}
	for _, r := range rows {
		got[r.Username] = r
	}
	if len(got) != 3 {
		t.Fatalf("expected alice, bob, carol only (no suspended, no bad context), got %+v", rows)
	}
	if !got["bob"].Excluded || got["alice"].Excluded {
		t.Fatalf("expected only bob excluded, got %+v", rows)
	}
	if got["carol"].Context != "carol" || got["bob"].Context != "bob-ctx" {
		t.Fatalf("unexpected contexts: %+v", rows)
	}
}

func TestBackupsApplyExclusionsCreatesAndRemovesMarkers(t *testing.T) {
	home := withScratchUserHomeBase(t)
	for _, d := range []string{"alice", "bob-ctx", "carol"} {
		os.MkdirAll(filepath.Join(home, d), 0755)
	}
	os.WriteFile(filepath.Join(home, "bob-ctx", "backup.off"), nil, 0644)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectExclusionUsers(mock)

	// exclude alice, un-exclude bob, unknown/bad names are ignored
	changed, err := backupsApplyExclusions(db, map[string]bool{"alice": true, "evil": true, "nobody": true})
	if err != nil {
		t.Fatal(err)
	}
	if changed != 2 {
		t.Fatalf("expected 2 changes, got %d", changed)
	}
	if _, err := os.Stat(filepath.Join(home, "alice", "backup.off")); err != nil {
		t.Fatalf("expected alice/backup.off to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "bob-ctx", "backup.off")); !os.IsNotExist(err) {
		t.Fatalf("expected bob-ctx/backup.off removed, stat err %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(home), "etc", "backup.off")); !os.IsNotExist(err) {
		t.Fatal("bad context must never be written to")
	}
}

func TestBackupsListExclusionsNoDB(t *testing.T) {
	if _, err := backupsListExclusions(nil); err == nil {
		t.Fatal("expected error without a database")
	}
}

func TestServeUserBackupsRendersExcludeAccountsTab(t *testing.T) {
	home := withScratchUserHomeBase(t)
	os.MkdirAll(filepath.Join(home, "bob-ctx"), 0755)
	os.WriteFile(filepath.Join(home, "bob-ctx", "backup.off"), nil, 0644)
	dir := t.TempDir()
	origCron, origEnv := CronFilePath, BackupEnvPath
	CronFilePath, BackupEnvPath = filepath.Join(dir, "openpanel"), filepath.Join(dir, "backup.env")
	t.Cleanup(func() { CronFilePath, BackupEnvPath = origCron, origEnv })

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectExclusionUsers(mock)

	b := &Backups{Sessions: auth.NewManager("test-secret", false), MySQL: db}
	rec := httptest.NewRecorder()
	b.ServeUserBackups(rec, httptest.NewRequest(http.MethodGet, "/backups/user", nil))

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, truncate(body))
	}
	for _, want := range []string{"Exclude Accounts", `action="/backups/user/exclusions"`, `value="bob" checked`, `data-user="alice"`, `name="mode" value="form"`, `id="destination"`, "Advanced", "</html>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(body))
		}
	}
	if strings.Contains(body, "SUSPENDED_123_dave") {
		t.Fatal("suspended users should not be listed")
	}
}
