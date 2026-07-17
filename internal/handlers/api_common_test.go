package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/golang-jwt/jwt/v5"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/config"
)

func newAPITestDB(t *testing.T) *admindb.DB {
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
	return db
}

func TestCreateAndParseAPIToken(t *testing.T) {
	token, err := createAPIToken("alice", "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	username, ok := parseAPIToken(req, "test-secret")
	if !ok || username != "alice" {
		t.Fatalf("expected (alice, true), got (%q, %v)", username, ok)
	}
}

func TestParseAPITokenRejectsWrongSecret(t *testing.T) {
	token, err := createAPIToken("alice", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	if _, ok := parseAPIToken(req, "wrong-secret"); ok {
		t.Fatal("expected token signed with a different secret to be rejected")
	}
}

func TestParseAPITokenRejectsMissingOrMalformedHeader(t *testing.T) {
	cases := []string{"", "Bearer ", "NotBearer sometoken", "sometoken"}
	for _, h := range cases {
		req := httptest.NewRequest("GET", "/api/whoami", nil)
		if h != "" {
			req.Header.Set("Authorization", h)
		}
		if _, ok := parseAPIToken(req, "test-secret"); ok {
			t.Fatalf("expected header %q to be rejected", h)
		}
	}
}

func TestParseAPITokenRejectsExpiredToken(t *testing.T) {
	now := time.Now().Add(-2 * APIJWTExpiry)
	claims := apiJWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "alice",
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if _, ok := parseAPIToken(req, "test-secret"); ok {
		t.Fatal("expected an expired token to be rejected")
	}
}

func createAPIAuthTestUser(t *testing.T, db *admindb.DB, username, role string) string {
	t.Helper()
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

func TestRequireAPITokenRejectsMissingToken(t *testing.T) {
	a := &APIAuth{DB: newAPITestDB(t), SecretKey: "test-secret"}
	called := false
	h := a.RequireAPIToken(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest("GET", "/api/whoami", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if called {
		t.Fatal("handler should not have been called without a token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAPITokenAllowsValidTokenAndSetsContext(t *testing.T) {
	db := newAPITestDB(t)
	a := &APIAuth{DB: db, SecretKey: "test-secret"}
	token := createAPIAuthTestUser(t, db, "alice", "admin")

	var gotUsername string
	h := a.RequireAPIToken(func(w http.ResponseWriter, r *http.Request) {
		gotUsername = APIUserFromContext(r).Username
	})

	req := httptest.NewRequest("GET", "/api/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h(rec, req)

	if gotUsername != "alice" {
		t.Fatalf("expected handler to see user alice in context, got %q", gotUsername)
	}
}

// TestRequireAPITokenAllowsTokenForDeletedUser locks in the loosened
// RequireAPIToken behavior: a bare @jwt_required() route in the original
// (e.g. /api/whoami) never looks up the acting user's DB row at all, so a
// still-valid token for a since-deleted user must still reach the
// handler -- APIUserFromContext is simply nil, and callers that need the
// identity use APIUsernameFromContext instead.
func TestRequireAPITokenAllowsTokenForDeletedUser(t *testing.T) {
	a := &APIAuth{DB: newAPITestDB(t), SecretKey: "test-secret"}
	token, err := createAPIToken("ghost", "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	var gotUser *admindb.User
	var gotUsername string
	called := false
	h := a.RequireAPIToken(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotUser = APIUserFromContext(r)
		gotUsername = APIUsernameFromContext(r)
	})

	req := httptest.NewRequest("GET", "/api/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h(rec, req)

	if !called {
		t.Fatalf("expected the handler to run for a valid token even without a matching DB user, got status %d", rec.Code)
	}
	if gotUser != nil {
		t.Fatalf("expected a nil user for a deleted account, got %+v", gotUser)
	}
	if gotUsername != "ghost" {
		t.Fatalf("expected username %q from context, got %q", "ghost", gotUsername)
	}
}

func TestRequireAPIAdminReturns404ForDeletedUser(t *testing.T) {
	a := &APIAuth{DB: newAPITestDB(t), SecretKey: "test-secret"}
	token, err := createAPIToken("ghost", "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	called := false
	h := a.RequireAPIAdmin(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h(rec, req)

	if called {
		t.Fatal("handler should not run for a token whose user no longer exists")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (matching api_admin_required's 'User not found' check), got %d", rec.Code)
	}
}

func TestRequireAPIAdminBlocksReseller(t *testing.T) {
	db := newAPITestDB(t)
	a := &APIAuth{DB: db, SecretKey: "test-secret"}
	token := createAPIAuthTestUser(t, db, "bob", "reseller")

	called := false
	h := a.RequireAPIAdmin(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h(rec, req)

	if called {
		t.Fatal("reseller should be blocked by RequireAPIAdmin")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestRequireAPIAdminAllowsAdminAndUser(t *testing.T) {
	for _, role := range []string{"admin", "user"} {
		db := newAPITestDB(t)
		a := &APIAuth{DB: db, SecretKey: "test-secret"}
		token := createAPIAuthTestUser(t, db, "u-"+role, role)

		called := false
		h := a.RequireAPIAdmin(func(w http.ResponseWriter, r *http.Request) { called = true })

		req := httptest.NewRequest("GET", "/api/x", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h(rec, req)

		if !called {
			t.Fatalf("role %q should be allowed by RequireAPIAdmin, got status %d", role, rec.Code)
		}
	}
}

func TestRequireAPISuperAdminOnlyAllowsAdmin(t *testing.T) {
	db := newAPITestDB(t)
	a := &APIAuth{DB: db, SecretKey: "test-secret"}
	adminToken := createAPIAuthTestUser(t, db, "root-admin", "admin")
	userToken := createAPIAuthTestUser(t, db, "plain-user", "user")

	h := a.RequireAPISuperAdmin(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected admin to be allowed, got %d", rec.Code)
	}

	req = httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected non-admin to be forbidden, got %d", rec.Code)
	}
}

func TestRequireAPIOwnerOrAdminAllowsNonResellerUnconditionally(t *testing.T) {
	db := newAPITestDB(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	a := &APIAuth{DB: db, MySQL: mysqlDB, SecretKey: "test-secret"}
	token := createAPIAuthTestUser(t, db, "admin1", "admin")

	called := false
	h := a.RequireAPIOwnerOrAdmin("username", func(w http.ResponseWriter, r *http.Request) { called = true })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/users/{username}", h)
	req := httptest.NewRequest("GET", "/api/users/someoneelse", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if !called {
		t.Fatalf("expected admin to bypass ownership check, got status %d", rec.Code)
	}
}

func TestRequireAPIOwnerOrAdminChecksResellerOwnership(t *testing.T) {
	db := newAPITestDB(t)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT 1 FROM users`).
		WithArgs("bob", "reseller1").
		WillReturnRows(sqlmock.NewRows([]string{"1"})) // no rows added -> not owner

	a := &APIAuth{DB: db, MySQL: mysqlDB, SecretKey: "test-secret"}
	token := createAPIAuthTestUser(t, db, "reseller1", "reseller")

	called := false
	h := a.RequireAPIOwnerOrAdmin("username", func(w http.ResponseWriter, r *http.Request) { called = true })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/users/{username}", h)
	req := httptest.NewRequest("GET", "/api/users/bob", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if called {
		t.Fatal("reseller should be blocked for a user they don't own")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

// withScratchOpenpanelConfigForAPI points config.OpenpanelConfigPath at a
// scratch file with both the [PANEL] api toggle and [LICENSE] key that
// apiFeatureEnabled reads -- both fresh (not through config.Openpanel()'s
// cache), so this is safe to vary test-to-test.
func withScratchOpenpanelConfigForAPI(t *testing.T, panelAPI, licenseKey string) {
	t.Helper()
	dir := t.TempDir()
	origPath := config.OpenpanelConfigPath
	path := filepath.Join(dir, "openpanel.config")
	content := "[PANEL]\napi=" + panelAPI + "\n\n[LICENSE]\nkey=" + licenseKey + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	config.OpenpanelConfigPath = path
	t.Cleanup(func() { config.OpenpanelConfigPath = origPath })
}

func TestRequireAPIFeatureEnabledBlocksWhenNoLicense(t *testing.T) {
	withScratchOpenpanelConfigForAPI(t, "on", "")

	called := false
	h := RequireAPIFeatureEnabled(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest("GET", "/api/", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if called {
		t.Fatal("expected feature-disabled without a license key")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRequireAPIFeatureEnabledBlocksWhenAPIOff(t *testing.T) {
	withScratchOpenpanelConfigForAPI(t, "off", "some-license-key")

	called := false
	h := RequireAPIFeatureEnabled(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest("GET", "/api/", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if called {
		t.Fatal("expected feature-disabled when [PANEL] api is off")
	}
}

func TestRequireAPIFeatureEnabledAllowsWhenLicensedAndOn(t *testing.T) {
	withScratchOpenpanelConfigForAPI(t, "on", "some-license-key")

	called := false
	h := RequireAPIFeatureEnabled(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest("GET", "/api/", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if !called {
		t.Fatalf("expected feature enabled, got status %d", rec.Code)
	}
}
