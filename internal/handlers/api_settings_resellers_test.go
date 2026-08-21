package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openadmin/internal/auth"
)

func newAPISettingsResellersHandler(t *testing.T) (*APISettingsResellers, func(username, role string) string) {
	t.Helper()
	db := newAPITestDB(t)
	a := &APIAuth{DB: db, SecretKey: "test-secret"}
	h := &APISettingsResellers{DB: db, Auth: a}
	return h, func(username, role string) string {
		hash, err := auth.GeneratePasswordHash("pw")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.CreateUser(username, hash, role); err != nil {
			t.Fatal(err)
		}
		token, err := createAPIToken(username, "test-secret")
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
}

func TestAPISettingsResellersGetBlocksResellerRole(t *testing.T) {
	h, createUser := newAPISettingsResellersHandler(t)
	token := createUser("reseller1", "reseller")

	req := httptest.NewRequest(http.MethodGet, "/api/settings/resellers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeSettingsResellers(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPISettingsResellersGetListsResellersForAdmin(t *testing.T) {
	h, createUser := newAPISettingsResellersHandler(t)
	adminToken := createUser("admin1", "admin")
	createUser("reseller1", "reseller")
	createUser("plainuser", "user")

	req := httptest.NewRequest(http.MethodGet, "/api/settings/resellers", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	h.ServeSettingsResellers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["username"] != "reseller1" {
		t.Fatalf("expected exactly one reseller1 row, got %v", rows)
	}
}

func TestAPISettingsResellersMissingTokenUserReturns404(t *testing.T) {
	h, _ := newAPISettingsResellersHandler(t)
	token, err := createAPIToken("ghost", "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/settings/resellers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeSettingsResellers(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAPISettingsResellersPostInvalidActionReturns400(t *testing.T) {
	h, createUser := newAPISettingsResellersHandler(t)
	adminToken := createUser("admin1", "admin")

	req := httptest.NewRequest(http.MethodPost, "/api/settings/resellers", strings.NewReader(`{"action":"bogus","username":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	h.ServeSettingsResellers(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPISettingsResellersPostCreateSucceeds(t *testing.T) {
	h, createUser := newAPISettingsResellersHandler(t)
	adminToken := createUser("admin1", "admin")

	req := httptest.NewRequest(http.MethodPost, "/api/settings/resellers",
		strings.NewReader(`{"action":"create","username":"newreseller","password":"secretpw"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	h.ServeSettingsResellers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&body)
	if body["success"] != true {
		t.Fatalf("expected success=true, got %v", body)
	}

	created, err := h.DB.UserByUsername("newreseller")
	if err != nil {
		t.Fatalf("expected newreseller to exist: %v", err)
	}
	if created.Role != "reseller" {
		t.Fatalf("expected role reseller, got %q", created.Role)
	}
}

func TestAPISettingsResellersPostResetPasswordAsResellerOverridesTargetToSelf(t *testing.T) {
	h, createUser := newAPISettingsResellersHandler(t)
	resellerToken := createUser("reseller1", "reseller")

	// Even though a different username/action is submitted, a reseller
	// caller can only ever reset their own password through this route.
	req := httptest.NewRequest(http.MethodPost, "/api/settings/resellers",
		strings.NewReader(`{"action":"delete","username":"someoneelse","new_password":"newsecretpw"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+resellerToken)
	rec := httptest.NewRecorder()
	h.ServeSettingsResellers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&body)
	if !strings.Contains(body["message"].(string), "reseller1") {
		t.Fatalf("expected message to reference reseller1 (self), got %v", body["message"])
	}

	updated, err := h.DB.UserByUsername("reseller1")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPasswordHash(updated.PasswordHash, "newsecretpw") {
		t.Fatal("expected reseller1's own password to have been changed")
	}
}

// TestAPISettingsResellersPostSuspendFailsWithoutOpenCLI locks in the
// deterministic failure path for actions that shell out to opencli --
// there's no opencli binary in the test sandbox, so this always 400s with
// the standard command-error message.
func TestAPISettingsResellersPostSuspendFailsWithoutOpenCLI(t *testing.T) {
	h, createUser := newAPISettingsResellersHandler(t)
	adminToken := createUser("admin1", "admin")
	createUser("reseller1", "reseller")

	req := httptest.NewRequest(http.MethodPost, "/api/settings/resellers",
		strings.NewReader(`{"action":"suspend","username":"reseller1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	h.ServeSettingsResellers(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (opencli not present), got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAPISettingsResellersPostUpdateBrandingAsResellerOverridesTargetToSelf
// mirrors TestAPISettingsResellersPostResetPasswordAsResellerOverridesTargetToSelf:
// unlike every other reseller-submitted action (forced to reset_password),
// update_branding is allowed through as-is, just with username forced to
// self. There's no opencli binary in the test sandbox, so this still 400s --
// what's being locked in here is that it reaches the update_branding branch
// (with username=self) instead of being silently downgraded to
// reset_password.
func TestAPISettingsResellersPostUpdateBrandingAsResellerOverridesTargetToSelf(t *testing.T) {
	h, createUser := newAPISettingsResellersHandler(t)
	resellerToken := createUser("reseller1", "reseller")

	req := httptest.NewRequest(http.MethodPost, "/api/settings/resellers",
		strings.NewReader(`{"action":"update_branding","username":"someoneelse","logo_url":"https://example.com/logo.png"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+resellerToken)
	rec := httptest.NewRecorder()
	h.ServeSettingsResellers(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (opencli not present), got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&body)
	if body["success"] == true {
		t.Fatalf("expected success=false without a real opencli binary, got %v", body)
	}

	if _, err := h.DB.UserByUsername("someoneelse"); err == nil {
		t.Fatal("expected 'someoneelse' to not exist -- update_branding must not have been redirected to another action against it")
	}
}

func TestAPISettingsResellersPostUpdateBrandingIsAValidAction(t *testing.T) {
	h, createUser := newAPISettingsResellersHandler(t)
	adminToken := createUser("admin1", "admin")
	createUser("reseller1", "reseller")

	req := httptest.NewRequest(http.MethodPost, "/api/settings/resellers",
		strings.NewReader(`{"action":"update_branding","username":"reseller1","logo_url":"https://example.com/logo.png"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	h.ServeSettingsResellers(rec, req)

	// 400 (no opencli binary in the sandbox) rather than the generic
	// "Missing required fields." 400 an unrecognized action would give --
	// distinguished by the error message.
	var body map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&body)
	if strings.Contains(body["error"].(string), "Missing required fields") {
		t.Fatalf("expected update_branding to be recognized as a valid action, got %v", body)
	}
}
