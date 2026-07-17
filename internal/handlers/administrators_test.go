package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/license"
)

func newAdministratorsTestServer(t *testing.T, admins *Administrators, loginAsRole string) (*httptest.Server, *http.Client, *admindb.DB) {
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
	admins.DB = db

	sessions := auth.NewManager("test-secret", false)
	admins.Sessions = sessions

	hash, _ := auth.GeneratePasswordHash("pw")
	if err := db.CreateUser("caller", hash, loginAsRole); err != nil {
		t.Fatal(err)
	}
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/administrators", admins.ServeAdministrators)
	mux.HandleFunc("/administrators/{action}/{username}", admins.ServeEditForm)
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

func TestAdministratorsListExcludesResellersAndIncludesDetails(t *testing.T) {
	admins := &Administrators{}
	srv, client, db := newAdministratorsTestServer(t, admins, "admin")

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("bob", hash, "user")
	db.CreateUser("alice-reseller", hash, "reseller")
	db.SetTOTP("bob", "SECRET", true)

	resp, err := client.Get(srv.URL + "/administrators?output=json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var rows []administratorRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("failed to decode %s: %v", body, err)
	}

	names := map[string]administratorRow{}
	for _, row := range rows {
		names[row.Username] = row
	}
	if _, ok := names["alice-reseller"]; ok {
		t.Fatal("expected reseller accounts to be excluded from the administrators list")
	}
	if bob, ok := names["bob"]; !ok || !bob.TOTPEnabled {
		t.Fatalf("expected bob to appear with totp_enabled=true, got %+v", names["bob"])
	}
}

func TestAdministratorsCreateBlockedOnCommunity(t *testing.T) {
	admins := &Administrators{} // LicenseChecker nil -> Community
	srv, client, _ := newAdministratorsTestServer(t, admins, "admin")

	resp, err := client.PostForm(srv.URL+"/administrators", url.Values{
		"action": {"create"}, "username": {"newadmin"}, "password": {"pw123456"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Community edition supports only one Administrator account.") {
		t.Fatalf("expected Community-edition rejection flash, got %s", body)
	}

	if _, err := admins.DB.UserByUsername("newadmin"); err == nil {
		t.Fatal("expected no account to have been created on Community edition")
	}
}

func TestAdministratorsCreateAllowedOnEnterprise(t *testing.T) {
	admins := &Administrators{}
	srv, client, db := newAdministratorsTestServer(t, admins, "admin")

	// simulate a validated Enterprise license without a real network call
	withMockLicenseAPI(t, "Active")
	admins.LicenseChecker = license.NewChecker("enterprise-test", "203.0.113.1")

	resp, err := client.PostForm(srv.URL+"/administrators", url.Values{
		"action": {"create"}, "username": {"newadmin"}, "password": {"pw123456"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	u, err := db.UserByUsername("newadmin")
	if err != nil {
		t.Fatalf("expected account to be created on Enterprise: %v", err)
	}
	if !auth.CheckPasswordHash(u.PasswordHash, "pw123456") {
		t.Fatal("expected the created account's password to verify")
	}
}

func withMockLicenseAPI(t *testing.T, status string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<response><status>"+status+"</status></response>")
	}))
	t.Cleanup(srv.Close)
	orig := license.APIURL
	license.APIURL = srv.URL
	t.Cleanup(func() { license.APIURL = orig })

	origCache := license.CacheFilePath
	license.CacheFilePath = filepath.Join(t.TempDir(), "license_cache.json")
	t.Cleanup(func() { license.CacheFilePath = origCache })
}

func TestUpdatePasswordForUserPermissions(t *testing.T) {
	admins := &Administrators{}
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })
	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	admins.DB = db

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("superadmin", hash, "admin")
	db.CreateUser("plainuser", hash, "user")

	superadmin, _ := db.UserByUsername("superadmin")
	plainuser, _ := db.UserByUsername("plainuser")

	// a plain "user" may not reset the Super Administrator's password
	if admins.updatePasswordForUser("superadmin", "newpw123", plainuser) {
		t.Fatal("expected a 'user' role to be denied resetting the admin's password")
	}

	// the admin can reset anyone's password
	if !admins.updatePasswordForUser("plainuser", "newpw123", superadmin) {
		t.Fatal("expected the admin role to be able to reset another account's password")
	}
	updated, _ := db.UserByUsername("plainuser")
	if !auth.CheckPasswordHash(updated.PasswordHash, "newpw123") {
		t.Fatal("expected the password to actually be updated")
	}
}

func TestDisable2FARequiresAdminAndNotSelf(t *testing.T) {
	admins := &Administrators{}
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })
	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	admins.DB = db

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("superadmin", hash, "admin")
	db.CreateUser("bob", hash, "user")
	db.SetTOTP("bob", "SECRET", true)
	db.SetTOTP("superadmin", "SECRET", true)

	superadmin, _ := db.UserByUsername("superadmin")
	bob, _ := db.UserByUsername("bob")

	if admins.disable2FAForUser("bob", bob) {
		t.Fatal("expected a non-admin caller to be denied")
	}
	if admins.disable2FAForUser("superadmin", superadmin) {
		t.Fatal("expected an admin to be denied disabling their own 2FA through this path")
	}
	if !admins.disable2FAForUser("bob", superadmin) {
		t.Fatal("expected the admin to be able to disable another user's 2FA")
	}
	updated, _ := db.UserByUsername("bob")
	if updated.TOTPEnabled {
		t.Fatal("expected totp_enabled to be cleared")
	}
}

func TestAdminUsernameAndPasswordValidation(t *testing.T) {
	admins := &Administrators{}
	srv, client, _ := newAdministratorsTestServer(t, admins, "admin")

	resp, err := client.PostForm(srv.URL+"/administrators", url.Values{
		"action": {"create"}, "username": {"bad name!"}, "password": {"pw123456"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Username can only contain letters, numbers, and underscores") {
		t.Fatalf("expected username validation error, got %s", body)
	}

	resp, err = client.PostForm(srv.URL+"/administrators", url.Values{
		"action": {"create"}, "username": {"gooduser"}, "password": {"short"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Password must be 6-30 characters") {
		t.Fatalf("expected password validation error, got %s", body)
	}
}

func TestServeEditFormRendersHTML(t *testing.T) {
	admins := &Administrators{}
	srv, client, db := newAdministratorsTestServer(t, admins, "admin")

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("bob", hash, "user")

	for _, tc := range []struct {
		action string
		want   []string
	}{
		{"rename", []string{"Rename Administrator: bob", "New Username", "</html>"}},
		{"password", []string{"Change Password for bob", "generatePassword", "</html>"}},
	} {
		resp, err := client.Get(srv.URL + "/administrators/" + tc.action + "/bob")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", tc.action, resp.StatusCode, truncate(string(body)))
		}
		got := string(body)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Fatalf("%s: expected page to contain %q (page may have been truncated by a template execution error), got %s", tc.action, want, truncate(got))
			}
		}
	}
}

func TestServeEditFormPermissions(t *testing.T) {
	admins := &Administrators{}
	srv, client, db := newAdministratorsTestServer(t, admins, "user") // caller is a plain "user"

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("superadmin", hash, "admin")

	// a plain "user" caller may not edit the "admin"-role account
	resp, err := client.Get(srv.URL + "/administrators/rename/superadmin")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Super Administrator and Reseller users can not be edited") {
		t.Fatalf("expected permission-denied flash, got %s", body)
	}

	// an invalid action redirects with an error
	resp, err = client.Get(srv.URL + "/administrators/nonsense/superadmin")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "can only be renamed or password changed") {
		t.Fatalf("expected invalid-action flash, got %s", body)
	}
}
