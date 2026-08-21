package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/paneldb"
)

func withScratchResellersConfigDir(t *testing.T) {
	t.Helper()
	orig := paneldb.ResellerConfigDir
	paneldb.ResellerConfigDir = t.TempDir()
	t.Cleanup(func() { paneldb.ResellerConfigDir = orig })

	// Resellers are off by default (resellersEnabled() in resellers.go);
	// point admin.ini at a scratch file with them already turned on so
	// existing tests exercising create/update/etc. don't all need to know
	// about the master switch -- TestServeResellersToggle* below cover that
	// switch itself directly.
	origAdminConfig := config.AdminConfigPath
	config.AdminConfigPath = filepath.Join(t.TempDir(), "admin.ini")
	data := config.Data{}
	data.Set("RESELLERS", "enabled", "yes")
	if err := config.Save(config.AdminConfigPath, data); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { config.AdminConfigPath = origAdminConfig })
}

func newResellersTestServer(t *testing.T, rs *Resellers, loginAsRole string) (*httptest.Server, *http.Client, *admindb.DB) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rs.DB = db

	hash, _ := auth.GeneratePasswordHash("pw")
	if err := db.CreateUser("caller", hash, loginAsRole); err != nil {
		t.Fatal(err)
	}
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	rs.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("/resellers", rs.ServeResellers)
	mux.HandleFunc("/resellers/{action}/{username}", rs.ServeEditForm)
	mux.HandleFunc("/account", rs.ServeAccount)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
	})

	handler := auth.WithUserLoader(sessions, db)(mux)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	if _, err := client.Get(srv.URL + "/login-as"); err != nil {
		t.Fatal(err)
	}
	return srv, client, db
}

func TestServeResellersListExcludesNonResellers(t *testing.T) {
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, db := newResellersTestServer(t, rs, "admin")

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("bob-reseller", hash, "reseller")
	db.CreateUser("regular-user", hash, "user")

	resp, err := client.Get(srv.URL + "/resellers?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if !strings.Contains(got, "bob-reseller") {
		t.Fatalf("expected reseller listed, got %s", got)
	}
	if strings.Contains(got, "regular-user") {
		t.Fatalf("expected non-reseller excluded, got %s", got)
	}
}

func TestServeResellersPostMissingFieldsFlash(t *testing.T) {
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client := newResellersTestServer2(t, rs, "admin")

	resp, err := client.PostForm(srv.URL+"/resellers", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Error: Missing required fields.") {
		t.Fatalf("expected missing-fields flash, got %s", truncate(string(body)))
	}
}

func newResellersTestServer2(t *testing.T, rs *Resellers, role string) (*httptest.Server, *http.Client) {
	srv, client, _ := newResellersTestServer(t, rs, role)
	return srv, client
}

func TestServeResellersPostCreate(t *testing.T) {
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, db := newResellersTestServer(t, rs, "admin")

	resp, err := client.PostForm(srv.URL+"/resellers", url.Values{
		"action": {"create"}, "username": {"newreseller"}, "password": {"secret123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Successfully created a new reseller user: newreseller") {
		t.Fatalf("expected success flash without 'Success:' prefix, got %s", truncate(string(body)))
	}
	u, err := db.UserByUsername("newreseller")
	if err != nil {
		t.Fatalf("expected reseller created: %v", err)
	}
	if u.Role != "reseller" {
		t.Fatalf("expected role reseller, got %q", u.Role)
	}
}

// TestServeResellersCreateBlockedWhenDisabled deliberately does NOT call
// withScratchResellersConfigDir (which enables resellers) -- it points
// AdminConfigPath at an empty scratch file instead, so resellersEnabled()
// falls back to its "no" default.
func TestServeResellersCreateBlockedWhenDisabled(t *testing.T) {
	origAdminConfig := config.AdminConfigPath
	config.AdminConfigPath = filepath.Join(t.TempDir(), "admin.ini")
	t.Cleanup(func() { config.AdminConfigPath = origAdminConfig })

	rs := &Resellers{}
	srv, client, db := newResellersTestServer(t, rs, "admin")

	resp, err := client.PostForm(srv.URL+"/resellers", url.Values{
		"action": {"create"}, "username": {"newreseller"}, "password": {"secret123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Error: Resellers are disabled.") {
		t.Fatalf("expected disabled-resellers error flash, got %s", truncate(string(body)))
	}
	if _, err := db.UserByUsername("newreseller"); err == nil {
		t.Fatal("expected no reseller to have been created")
	}
}

func TestServeResellersToggleEnableThenDisable(t *testing.T) {
	origAdminConfig := config.AdminConfigPath
	config.AdminConfigPath = filepath.Join(t.TempDir(), "admin.ini")
	t.Cleanup(func() { config.AdminConfigPath = origAdminConfig })

	// paneldb.ResellerConfigDir also needs a scratch dir for the reseller
	// account files created/deleted below, but we do NOT want
	// withScratchResellersConfigDir's AdminConfigPath override (it defaults
	// resellers to enabled) -- so replicate just the ResellerConfigDir half.
	origResellerDir := paneldb.ResellerConfigDir
	paneldb.ResellerConfigDir = t.TempDir()
	t.Cleanup(func() { paneldb.ResellerConfigDir = origResellerDir })

	rs := &Resellers{}
	srv, client, db := newResellersTestServer(t, rs, "admin")

	if resellersEnabled() {
		t.Fatal("expected disabled by default")
	}

	resp, err := client.PostForm(srv.URL+"/resellers", url.Values{"action": {"enable_resellers"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Resellers enabled.") {
		t.Fatalf("expected enabled success flash, got %s", truncate(string(body)))
	}
	if !resellersEnabled() {
		t.Fatal("expected resellersEnabled() to be true after enabling")
	}

	// Create one reseller, then disabling should be refused.
	if _, err := client.PostForm(srv.URL+"/resellers", url.Values{
		"action": {"create"}, "username": {"blocker"}, "password": {"secret123"},
	}); err != nil {
		t.Fatal(err)
	}

	resp, err = client.PostForm(srv.URL+"/resellers", url.Values{"action": {"disable_resellers"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Cannot disable resellers while 1 reseller account(s) still exist") {
		t.Fatalf("expected blocked-disable flash, got %s", truncate(string(body)))
	}
	if !resellersEnabled() {
		t.Fatal("expected resellersEnabled() to still be true after a blocked disable")
	}

	// Remove the reseller directly (the "delete" action shells out to
	// opencli, which isn't available in this sandbox), then disabling
	// should succeed.
	if err := db.DeleteUser("blocker"); err != nil {
		t.Fatal(err)
	}

	resp, err = client.PostForm(srv.URL+"/resellers", url.Values{"action": {"disable_resellers"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Resellers disabled.") {
		t.Fatalf("expected disabled success flash, got %s", truncate(string(body)))
	}
	if resellersEnabled() {
		t.Fatal("expected resellersEnabled() to be false after disabling")
	}
}

func TestServeResellersPostUnknownActionFlashPrefixed(t *testing.T) {
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client := newResellersTestServer2(t, rs, "admin")

	// "bogus" isn't in resellerValidActions -> "Missing required fields"
	// since the action-validity check happens before runAction.
	resp, err := client.PostForm(srv.URL+"/resellers", url.Values{
		"action": {"bogus"}, "username": {"x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Error: Missing required fields.") {
		t.Fatalf("expected missing-fields flash for an unrecognized action, got %s", truncate(string(body)))
	}
}

func TestServeResellersPostAsResellerForcesSelfResetAndNowSucceeds(t *testing.T) {
	// Whatever action a reseller submitted, the POST handler forces
	// action="reset_password" and username=self. Administrators.
	// updatePasswordForUser allows a reseller to change only their own
	// password, so this self-service reset succeeds.
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, db := newResellersTestServer(t, rs, "reseller")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/resellers", url.Values{
		"action": {"delete"}, "username": {"someone-else"}, "new_password": {"newpass123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(resp.Request.URL.Path, "/account") {
		t.Fatalf("expected final URL to be /account, got %s", resp.Request.URL.Path)
	}
	if !strings.Contains(string(body), "Password changed for reseller user: caller") {
		t.Fatalf("expected the success flash, got %s", truncate(string(body)))
	}

	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPasswordHash(caller.PasswordHash, "newpass123") {
		t.Fatal("expected the reseller's own password to have actually changed")
	}
	if _, err := db.UserByUsername("someone-else"); err == nil {
		t.Fatal("expected 'someone-else' to NOT have been deleted (action was forced to reset_password)")
	}
}

func TestUpdatePasswordForUserResellerCannotTouchOthers(t *testing.T) {
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = dir + "/users.db"
	t.Cleanup(func() { admindb.Path = origPath })
	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("reseller-a", hash, "reseller")
	db.CreateUser("reseller-b", hash, "reseller")

	callerA, err := db.UserByUsername("reseller-a")
	if err != nil {
		t.Fatal(err)
	}

	admins := &Administrators{DB: db}
	if admins.updatePasswordForUser("reseller-b", "newpass123", callerA) {
		t.Fatal("expected a reseller to be rejected when targeting a different account")
	}
	if !admins.updatePasswordForUser("reseller-a", "newpass123", callerA) {
		t.Fatal("expected a reseller to be allowed to change their own password")
	}
}

func TestServeResellersEditFormInvalidAction(t *testing.T) {
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, db := newResellersTestServer(t, rs, "admin")
	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("bob-reseller", hash, "reseller")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Get(srv.URL + "/resellers/bogus/bob-reseller")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected a redirect for invalid action, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/resellers" {
		t.Fatalf("expected redirect to /resellers, got %q", loc)
	}
}

func TestServeResellersEditFormNonResellerRejected(t *testing.T) {
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, db := newResellersTestServer(t, rs, "admin")
	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("plain-admin", hash, "admin")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Get(srv.URL + "/resellers/rename/plain-admin")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", resp.StatusCode)
	}
}

func TestServeResellersEditFormRenamePage(t *testing.T) {
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, db := newResellersTestServer(t, rs, "admin")
	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("bob-reseller", hash, "reseller")

	resp, err := client.Get(srv.URL + "/resellers/rename/bob-reseller")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "bob-reseller") {
		t.Fatalf("expected rename page to show the username, got %s", truncate(string(body)))
	}
}

func TestServeResellersEditFormUpdatePageIncludesResellerData(t *testing.T) {
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, db := newResellersTestServer(t, rs, "admin")
	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("bob-reseller", hash, "reseller")
	os.WriteFile(filepath.Join(paneldb.ResellerConfigDir, "bob-reseller.json"),
		[]byte(`{"max_accounts": 5, "current_accounts": 2, "allowed_plans": [1,2], "current_disk_blocks": 10, "max_disk_blocks": 100}`), 0644)

	resp, err := client.Get(srv.URL + "/resellers/update/bob-reseller")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `value="5"`) {
		t.Fatalf("expected max_accounts=5 pre-filled, got %s", truncate(string(body)))
	}
}

func TestServeAccountForbiddenForNonReseller(t *testing.T) {
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, _ := newResellersTestServer(t, rs, "admin")

	resp, err := client.Get(srv.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-reseller, got %d", resp.StatusCode)
	}
}

func TestServeAccountAllowedForReseller(t *testing.T) {
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, _ := newResellersTestServer(t, rs, "reseller")

	resp, err := client.Get(srv.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Change your reseller account password.") {
		t.Fatalf("expected self-service copy, got %s", truncate(string(body)))
	}
	if !strings.Contains(string(body), `name="logo_url"`) {
		t.Fatalf("expected a Branding section with a logo_url field, got %s", truncate(string(body)))
	}
}

func TestServeResellersEditFormUpdatePageIncludesBrandingSection(t *testing.T) {
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, db := newResellersTestServer(t, rs, "admin")
	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("bob-reseller", hash, "reseller")
	os.WriteFile(filepath.Join(paneldb.ResellerConfigDir, "bob-reseller.json"),
		[]byte(`{"max_accounts": 5, "logo_url": "https://example.com/logo.png"}`), 0644)

	resp, err := client.Get(srv.URL + "/resellers/update/bob-reseller")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "https://example.com/logo.png") {
		t.Fatalf("expected the reseller's logo_url pre-filled, got %s", truncate(string(body)))
	}
}

func TestServeResellersUpdateBrandingIsAValidAction(t *testing.T) {
	// "update_branding" must pass the resellerValidActions gate -- confirmed
	// by NOT getting the "Missing required fields" flash that unknown or
	// disallowed actions get. It'll still fail past that point since no real
	// opencli binary is on PATH in tests, but that's a separate concern
	// (exercised by admin-only actions like "update"/"rename_user" too,
	// which have no dedicated success test for the same reason).
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, db := newResellersTestServer(t, rs, "admin")
	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("bob-reseller", hash, "reseller")

	resp, err := client.PostForm(srv.URL+"/resellers", url.Values{
		"action": {"update_branding"}, "username": {"bob-reseller"}, "logo_url": {"https://example.com/logo.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "Missing required fields") {
		t.Fatalf("expected update_branding to be recognized as a valid action, got %s", truncate(string(body)))
	}
}

func TestServeResellersPostAsResellerUpdateBrandingForcesSelfUsername(t *testing.T) {
	// A reseller submitting update_branding with no username at all (or
	// someone else's) still passes the "username required" check below,
	// because handlePost forces username = self for this action before that
	// check runs -- if it hadn't, this would flash "Missing required fields"
	// instead of falling through to runAction (and failing there only
	// because no real opencli binary is on PATH in tests).
	withScratchResellersConfigDir(t)
	rs := &Resellers{}
	srv, client, _ := newResellersTestServer(t, rs, "reseller")

	resp, err := client.PostForm(srv.URL+"/resellers", url.Values{
		"action": {"update_branding"}, "logo_url": {"https://example.com/logo.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "Missing required fields") {
		t.Fatalf("expected username to have been forced to self, got %s", truncate(string(body)))
	}
}
