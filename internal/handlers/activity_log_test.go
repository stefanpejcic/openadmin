package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"openadmin/internal/activity"
	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

// withTestUser stands in for auth.WithUserLoader
func withTestUser(t *testing.T, user *admindb.User, next http.Handler) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, auth.WithCurrentUser(r, user))
	})
}

func TestActivityMiddleware(t *testing.T) {
	activity.Dir = t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /domains/add", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.FormValue("domain") == "bad.com" {
			activity.Fail(r.Context())
		}
		http.Redirect(w, r, "/domains", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /domains", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("POST /user/{action}/{username}", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("POST /forbidden", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusForbidden) })
	mux.HandleFunc("POST /api/users", func(w http.ResponseWriter, r *http.Request) {
		activity.SetActor(r.Context(), "apiadmin")
		w.WriteHeader(http.StatusCreated)
	})

	h := withTestUser(t, &admindb.User{Username: "stefan", Role: "admin"}, ActivityMiddleware(NotFoundHandler(mux)))
	post := func(path string, form url.Values) {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/domains", nil))
	post("/login", nil)
	post("/forbidden", nil)
	post("/nope", nil)
	post("/domains/add", url.Values{"domain": {"a.com"}, "username": {"john"}})
	post("/domains/add", url.Values{"domain": {"bad.com"}, "username": {"john"}})
	post("/user/suspend/john", nil)
	post("/api/users", nil)

	var got []string
	for _, e := range activity.Read("stefan") {
		got = append([]string{e.Action}, got...)
	}
	want := []string{
		"Added domain 'a.com' for user 'john'",
		"Added domain 'bad.com' for user 'john' (failed)",
		"Suspended user 'john'",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("expected %q, got %q", want, got)
	}
	api := activity.Read("apiadmin")
	if len(api) != 1 || api[0].Action != "POST /api/users (via API)" {
		t.Fatalf("expected the API call under the token's admin, got %+v", api)
	}
}

// a renamed route would silently fall back to "POST /path" in the log
func TestEveryDescribedRouteIsRegistered(t *testing.T) {
	src, err := os.ReadFile("../../cmd/openadmin/main.go")
	if err != nil {
		t.Fatal(err)
	}
	for pattern := range activityDescriptions {
		if !strings.Contains(string(src), `"`+pattern+`"`) {
			t.Errorf("described route %q isn't registered in main.go", pattern)
		}
	}
}
