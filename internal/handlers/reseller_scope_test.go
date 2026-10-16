package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
)

func testScope() *resellerScope {
	return &resellerScope{
		Reseller: "res1",
		users:    map[string]bool{"alice": true},
		domains:  map[string]string{"alice.com": "alice", "shop.alice.com": "alice"},
	}
}

func resellerRequest(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return withAPIUser(r, &admindb.User{Username: "res1", Role: "reseller"})
}

func expectScopeQueries(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT username FROM users WHERE owner = \?`).WithArgs("res1").
		WillReturnRows(sqlmock.NewRows([]string{"username"}).AddRow("alice"))
	mock.ExpectQuery(`SELECT d.domain_url, u.username FROM domains d`).WithArgs("res1").
		WillReturnRows(sqlmock.NewRows([]string{"domain_url", "username"}).AddRow("Alice.com", "alice"))
}

func TestScopeForAdminIsUnrestricted(t *testing.T) {
	r := withAPIUser(httptest.NewRequest(http.MethodGet, "/", nil), &admindb.User{Username: "admin1", Role: "admin"})
	s := scopeFor(r, nil)
	if s != nil || !s.AllowsDomain("anything.com") || !s.AllowsUser("bob") || !s.AllowsEmail("x@y.z") {
		t.Fatal("admins must not be restricted")
	}
}

func TestScopeForResellerLoadsOwnedUsersAndDomains(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	expectScopeQueries(mock)

	s := scopeFor(resellerRequest(http.MethodGet, "/", ""), db)
	if s == nil {
		t.Fatal("expected a scope for a reseller")
	}
	if !s.AllowsUser("alice") || s.AllowsUser("bob") {
		t.Fatal("wrong user scope")
	}
	if !s.AllowsDomain("alice.com") || !s.AllowsDomain("ALICE.COM") || s.AllowsDomain("bob.com") {
		t.Fatal("wrong domain scope")
	}
	if !s.AllowsEmail("info@alice.com") || s.AllowsEmail("info@bob.com") || s.AllowsEmail("alice.com") {
		t.Fatal("wrong email scope")
	}
	if s.AllowsEmails(nil) || s.AllowsEmails([]string{"a@alice.com", "b@bob.com"}) {
		t.Fatal("a list with a foreign address must be refused")
	}
}

func TestScopeForResellerWithoutDBGetsNothing(t *testing.T) {
	s := scopeFor(resellerRequest(http.MethodGet, "/", ""), nil)
	if s == nil || s.AllowsDomain("alice.com") || s.AllowsUser("alice") {
		t.Fatal("a reseller must never fall back to an unrestricted scope")
	}
}

func queueMsg(id, sender string, rcpts ...string) map[string]interface{} {
	var rs []interface{}
	for _, r := range rcpts {
		rs = append(rs, map[string]interface{}{"address": r})
	}
	return map[string]interface{}{"queue_id": id, "sender": sender, "recipients": rs}
}

func TestQueueVisibilityNeverLeaksOtherCustomers(t *testing.T) {
	s := testScope()
	queue := []map[string]interface{}{
		queueMsg("A1", "info@alice.com", "someone@gmail.com"),
		queueMsg("B2", "spam@evil.com", "info@alice.com"),
		queueMsg("C3", "spam@evil.com", "info@alice.com", "info@bob.com"),
		queueMsg("D4", "info@bob.com", "info@alice.com"),
		queueMsg("E5", "", ""),
	}
	var got []string
	for _, m := range s.filterQueue(queue) {
		got = append(got, m["queue_id"].(string))
	}
	// D4 only goes to the reseller's user, who sees that sender in their mailbox anyway, C3 would leak bob's address
	if strings.Join(got, ",") != "A1,B2,D4" {
		t.Fatalf("expected only A1,B2,D4 visible, got %v", got)
	}
}

func TestQueueTargets(t *testing.T) {
	s := testScope()
	queue := []map[string]interface{}{
		queueMsg("A1", "info@alice.com", "x@gmail.com"),
		queueMsg("D4", "info@bob.com", "y@gmail.com"),
	}
	if ids, ok := s.queueTargets(queue, "all", nil); !ok || strings.Join(ids, ",") != "A1" {
		t.Fatalf("all should mean only visible messages, got %v %v", ids, ok)
	}
	if _, ok := s.queueTargets(queue, "selected", []string{"A1", "D4"}); ok {
		t.Fatal("a foreign id must refuse the whole action")
	}
	if _, ok := s.queueTargets(queue, "selected", []string{"ALL"}); ok {
		t.Fatal("ALL must never pass as a queue id")
	}
}

func TestScopedBatchSkipsForeignItems(t *testing.T) {
	s := testScope()
	var ran []string
	batch := func(action, value string, items []string) ([]BulkResult, bool) {
		ran = items
		out := make([]BulkResult, len(items))
		for i, it := range items {
			out[i] = BulkResult{Item: it, OK: true}
		}
		return out, true
	}
	res, ok := scopedBatch(s, s.AllowsDomain, batch)("off", "", []string{"alice.com", "bob.com"})
	if !ok || strings.Join(ran, ",") != "alice.com" {
		t.Fatalf("batch must only run on owned items, ran %v", ran)
	}
	if !res[0].OK || res[1].OK {
		t.Fatalf("unexpected results: %+v", res)
	}
	ran = nil
	scopedBatch(s, s.AllowsDomain, batch)("off", "", []string{"bob.com"})
	if ran != nil {
		t.Fatal("batch must not run when nothing is owned")
	}
}

func TestResolveResellerBackupStaysInUploadFolder(t *testing.T) {
	dir := t.TempDir()
	orig := ImportUploadDir
	ImportUploadDir = dir
	t.Cleanup(func() { ImportUploadDir = orig })

	own := filepath.Join(dir, "res1")
	os.MkdirAll(own, 0700)
	os.WriteFile(filepath.Join(own, "alice_2026-01-01_00-00-00.tar.gz"), []byte("x"), 0600)
	outside := filepath.Join(dir, "other.tar.gz")
	os.WriteFile(outside, []byte("x"), 0600)
	os.Symlink(outside, filepath.Join(own, "link.tar.gz"))
	os.MkdirAll(filepath.Join(dir, "res2"), 0700)
	os.WriteFile(filepath.Join(dir, "res2", "b.tar.gz"), []byte("x"), 0600)

	if _, err := resolveResellerBackup("res1", "alice_2026-01-01_00-00-00.tar.gz"); err != nil {
		t.Fatalf("own backup refused: %v", err)
	}
	for _, p := range []string{outside, filepath.Join(own, "link.tar.gz"), "../res2/b.tar.gz", "/etc/passwd", own} {
		if _, err := resolveResellerBackup("res1", p); err == nil {
			t.Fatalf("%s should be refused", p)
		}
	}
	if got := listUploadedBackups("res1"); len(got) != 1 {
		t.Fatalf("expected only the real backup listed, symlinks are skipped, got %v", got)
	}
}

func TestImportLogReseller(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0600)
		return p
	}
	mine := write("a.log", "[2026-10-16 10:00:00] Log file: x\n[2026-10-16 10:00:00] Archive: y\n[2026-10-16 10:00:00] Reseller: res1\n")
	admin := write("b.log", "[2026-10-16 10:00:00] Log file: x\n")
	fake := write("c.log", "[2026-10-16 10:00:00] Domain 'Reseller: res1' skipped\n")
	if importLogReseller(mine) != "res1" || importLogReseller(admin) != "" || importLogReseller(fake) != "" {
		t.Fatal("wrong log owner detection")
	}
}

func TestAPIDomainActionRefusesForeignDomain(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	expectScopeQueries(mock)
	ran := false
	orig := apiRunCapture
	apiRunCapture = func(args ...string) (string, string, int) { ran = true; return "", "", 0 }
	t.Cleanup(func() { apiRunCapture = orig })

	r := resellerRequest(http.MethodPost, "/api/domains/delete/bob.com", "")
	r.SetPathValue("action", "delete")
	r.SetPathValue("domain", "bob.com")
	rec := httptest.NewRecorder()
	(&APIDomains{MySQL: db}).HandleDomainAction(rec, r)
	if rec.Code != http.StatusForbidden || ran {
		t.Fatalf("expected 403 without running opencli, got %d ran=%v", rec.Code, ran)
	}
}

func TestAPIDomainsListIsScoped(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(1, "/var/www/html/alice.com", "alice.com", "8.3", "alice").
		AddRow(2, "/var/www/html/bob.com", "bob.com", "8.3", "bob"))
	expectScopeQueries(mock)

	rec := httptest.NewRecorder()
	(&APIDomains{MySQL: db}).ServeDomains(rec, resellerRequest(http.MethodGet, "/api/domains", ""))
	var out struct {
		Domains []map[string]interface{} `json:"domains"`
	}
	json.NewDecoder(rec.Body).Decode(&out)
	if len(out.Domains) != 1 || out.Domains[0]["domain_url"] != "alice.com" {
		t.Fatalf("expected only alice.com, got %v", out.Domains)
	}
}

func TestUserActivityStatusScopedForReseller(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	expectScopeQueries(mock)
	orig := activeSessionUsernamesRun
	activeSessionUsernamesRun = func() (map[string]string, error) {
		return map[string]string{"alice": "active", "bob": "active"}, nil
	}
	t.Cleanup(func() { activeSessionUsernamesRun = orig })

	rec := httptest.NewRecorder()
	(&Dashboard{MySQL: db}).ServeUserActivityStatus(rec, resellerRequest(http.MethodGet, "/json/user_activity_status", ""))
	var out map[string]string
	json.NewDecoder(rec.Body).Decode(&out)
	if len(out) != 1 || out["alice"] != "active" {
		t.Fatalf("expected only alice, got %v", out)
	}
}

func TestServeIPsScopedForReseller(t *testing.T) {
	dir := t.TempDir()
	for user, ip := range map[string]string{"alice": "203.0.113.5", "bob": "203.0.113.6"} {
		os.MkdirAll(filepath.Join(dir, user), 0755)
		os.WriteFile(filepath.Join(dir, user, "ip.json"), []byte(`{"ip":"`+ip+`"}`), 0644)
	}
	orig := UserIPFilesGlob
	UserIPFilesGlob = filepath.Join(dir, "*", "ip.json")
	t.Cleanup(func() { UserIPFilesGlob = orig })

	db, mock, _ := sqlmock.New()
	defer db.Close()
	expectScopeQueries(mock)
	rec := httptest.NewRecorder()
	(&Users{MySQL: db}).ServeIPs(rec, resellerRequest(http.MethodGet, "/json/ips", ""))
	var out map[string]string
	json.NewDecoder(rec.Body).Decode(&out)
	if len(out) != 1 || out["alice"] != "203.0.113.5" {
		t.Fatalf("expected only alice's IP, got %v", out)
	}
}

func TestServeUserFilesRefusesForeignUser(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	expectScopeQueries(mock)
	r := resellerRequest(http.MethodGet, "/settings/defaults/files/bob", "")
	r.SetPathValue("username", "bob")
	rec := httptest.NewRecorder()
	(&Defaults{MySQL: db}).ServeUserFiles(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}
