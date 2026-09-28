package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/activity"
	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func TestAccountActivityResellerOnlySeesOwnLog(t *testing.T) {
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })
	activity.Dir = t.TempDir()

	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.CreateUser("res1", "x", "reseller")
	db.CreateUser("res2", "x", "reseller")
	activity.Record("res1", "203.0.113.1", "Created user 'john'")

	a := &AccountActivity{DB: db, Sessions: auth.NewManager("test-secret", false)}
	serve := func(role, self, target string) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /resellers/activity/{username}", a.Serve("/resellers"))
		req := auth.WithCurrentUser(httptest.NewRequest(http.MethodGet, "/resellers/activity/"+target, nil), &admindb.User{Username: self, Role: role})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := serve("reseller", "res2", "res1"); rec.Code != http.StatusForbidden {
		t.Fatalf("expected a reseller to get 403 on another reseller's log, got %d", rec.Code)
	}
	if rec := serve("reseller", "res1", "res1"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Created user &#39;john&#39;") {
		t.Fatalf("expected a reseller to see their own log, got %d", rec.Code)
	}
	if rec := serve("user", "admin2", "res1"); rec.Code != http.StatusOK {
		t.Fatalf("expected an admin to see any log, got %d", rec.Code)
	}
}
