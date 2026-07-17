package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/license"
)

func newAPISettingsAdministratorsTestServer(t *testing.T, role string) (*httptest.Server, *admindb.DB, string, *APISettingsAdministrators) {
	t.Helper()
	db := newAPITestDB(t)

	hash, _ := auth.GeneratePasswordHash("pw123456")
	db.CreateUser("caller", hash, role)
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	apiAuth := &APIAuth{DB: db, SecretKey: "test-secret"}
	a := &APISettingsAdministrators{DB: db}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings/administrators", apiAuth.RequireAPIAdmin(a.ServeSettingsAdministrators))
	mux.HandleFunc("POST /api/settings/administrators", apiAuth.RequireAPIAdmin(a.ServeSettingsAdministrators))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	token, err := createAPIToken(caller.Username, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	_ = caller
	return srv, db, token, a
}

func TestAPISettingsAdministratorsListExcludesResellers(t *testing.T) {
	srv, db, token, _ := newAPISettingsAdministratorsTestServer(t, "admin")
	hash, _ := auth.GeneratePasswordHash("pw123456")
	db.CreateUser("bob", hash, "user")
	db.CreateUser("res1", hash, "reseller")
	db.SetTOTP("bob", "SECRET", true)

	req := apiJSONRequest(t, http.MethodGet, srv.URL+"/api/settings/administrators", token, "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var rows []apiAdminRow
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	byName := map[string]apiAdminRow{}
	for _, r := range rows {
		byName[r.Username] = r
	}
	if _, ok := byName["res1"]; ok {
		t.Fatal("expected reseller excluded from the administrators list")
	}
	if bob, ok := byName["bob"]; !ok || !bob.TOTPEnabled || bob.LastIP != "N/A" {
		t.Fatalf("expected bob with totp_enabled and N/A last_ip, got %+v", byName["bob"])
	}
}

func TestAPISettingsAdministratorsResellerBlocked(t *testing.T) {
	srv, _, token, _ := newAPISettingsAdministratorsTestServer(t, "reseller")

	req := apiJSONRequest(t, http.MethodGet, srv.URL+"/api/settings/administrators", token, "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a reseller caller, got %d", resp.StatusCode)
	}
}

func TestAPISettingsAdministratorsMissingFields(t *testing.T) {
	srv, _, token, _ := newAPISettingsAdministratorsTestServer(t, "admin")

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/settings/administrators", token, `{"action":"bogus"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unrecognized action, got %d", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "Missing required fields." {
		t.Fatalf("expected 'Missing required fields.', got %+v", body)
	}
}

func TestAPISettingsAdministratorsUsernameValidation(t *testing.T) {
	srv, _, token, _ := newAPISettingsAdministratorsTestServer(t, "admin")

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/settings/administrators", token, `{"action":"create","username":"bad name!","password":"pw123456"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "Username can only contain letters, numbers, and underscores." {
		t.Fatalf("unexpected error message: %+v", body)
	}
}

func TestAPISettingsAdministratorsCreateBlockedOnCommunity(t *testing.T) {
	srv, db, token, _ := newAPISettingsAdministratorsTestServer(t, "admin") // LicenseChecker nil -> Community

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/settings/administrators", token, `{"action":"create","username":"newadmin","password":"pw123456"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "Community edition supports only one Administrator account." {
		t.Fatalf("unexpected body: %+v", body)
	}
	if _, err := db.UserByUsername("newadmin"); err == nil {
		t.Fatal("expected no account created on Community edition")
	}
}

func TestAPISettingsAdministratorsCreateAllowedOnEnterprise(t *testing.T) {
	srv, db, token, a := newAPISettingsAdministratorsTestServer(t, "admin")

	withMockLicenseAPI(t, "Active")
	a.LicenseChecker = license.NewChecker("enterprise-test", "203.0.113.1")

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/settings/administrators", token, `{"action":"create","username":"newadmin","password":"pw123456"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["success"] != true {
		t.Fatalf("expected success, got %+v", body)
	}
	if _, err := db.UserByUsername("newadmin"); err != nil {
		t.Fatalf("expected account created: %v", err)
	}
}

func TestAPISettingsAdministratorsResetPassword(t *testing.T) {
	srv, db, token, _ := newAPISettingsAdministratorsTestServer(t, "admin")
	hash, _ := auth.GeneratePasswordHash("pw123456")
	db.CreateUser("bob", hash, "user")

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/settings/administrators", token, `{"action":"reset_password","username":"bob","new_password":"newpass123"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["message"] != "Password changed for admin user: bob" {
		t.Fatalf("unexpected body: %+v", body)
	}
	updated, _ := db.UserByUsername("bob")
	if !auth.CheckPasswordHash(updated.PasswordHash, "newpass123") {
		t.Fatal("expected password to actually be updated")
	}
}

func TestAPISettingsAdministratorsDisable2FAFailureMessageHasNoUsername(t *testing.T) {
	// Only the Super Administrator can disable another account's 2FA, and
	// not even for their own account -- both rejections share the same
	// generic message (no username interpolated), unlike the HTML page's
	// equivalent flash.
	srv, db, token, _ := newAPISettingsAdministratorsTestServer(t, "user")
	hash, _ := auth.GeneratePasswordHash("pw123456")
	db.CreateUser("bob", hash, "user")
	db.SetTOTP("bob", "SECRET", true)

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/settings/administrators", token, `{"action":"disable_2fa","username":"bob"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "Failed disabling two-factor authentication. Only the Super Administrator can do this." {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestAPISettingsAdministratorsSuspendFallsBackWhenOpenCLIMissing(t *testing.T) {
	// No real opencli binary is available in the test environment, so this
	// exercises runOpenCLI's own "command not found" fallback path end to
	// end: a failed action still reports success:false with the shared
	// fallback error message, at 400.
	srv, db, token, _ := newAPISettingsAdministratorsTestServer(t, "admin")
	hash, _ := auth.GeneratePasswordHash("pw123456")
	db.CreateUser("bob", hash, "user")

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/settings/administrators", token, `{"action":"suspend","username":"bob"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["success"] != false || body["error"] == "" {
		t.Fatalf("expected a failure body, got %+v", body)
	}
}

func TestOpencliResultMessage(t *testing.T) {
	if got := opencliResultMessage(true, "created user bob"); got != "created user bob" {
		t.Fatalf("expected the command's own output, got %q", got)
	}
	if got := opencliResultMessage(true, ""); got != "OK" {
		t.Fatalf("expected 'OK' fallback for a successful command with no output, got %q", got)
	}
	if got := opencliResultMessage(false, "boom"); got != "boom" {
		t.Fatalf("expected the failure message preserved, got %q", got)
	}
}
