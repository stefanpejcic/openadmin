package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/config"
)

func withScratchErrorsConfig(t *testing.T, apiValue string) {
	t.Helper()
	dir := t.TempDir()
	origPath := config.OpenpanelConfigPath
	path := filepath.Join(dir, "openpanel.config")
	content := "[PANEL]\n"
	if apiValue != "" {
		content += "api=" + apiValue + "\n"
	}
	os.WriteFile(path, []byte(content), 0644)
	config.OpenpanelConfigPath = path
	t.Cleanup(func() {
		config.OpenpanelConfigPath = origPath
	})
}

func TestNotFoundHandlerServesMatchedRoute(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /known", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("known route"))
	})
	srv := httptest.NewServer(NotFoundHandler(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/known")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "known route" {
		t.Fatalf("expected the real route to still be served, got %q", body)
	}
}

// loggedIn puts an admin on every request, the error page only shows the panel to logged in users
func loggedIn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, auth.WithCurrentUser(r, &admindb.User{Username: "admin", Role: "admin"}))
	})
}

func TestNotFoundHandlerHTMLForUnmatchedRoute(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(loggedIn(NotFoundHandler(mux)))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	got := string(body)
	for _, want := range []string{"Page not found", "404", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected HTML error page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestNotFoundHandlerAnonymousGoesToLogin(t *testing.T) {
	rec := httptest.NewRecorder()
	NotFoundHandler(http.NewServeMux()).ServeHTTP(rec, httptest.NewRequest("GET", "/security/nope?x=1", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?next=%2Fsecurity%2Fnope%3Fx%3D1" {
		t.Fatalf("anonymous 404 should redirect to login, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if strings.Contains(rec.Body.String(), "</html>") {
		t.Fatal("anonymous 404 must not render the panel layout")
	}
}

func TestRecoverMiddlewareAnonymousPlainError(t *testing.T) {
	rec := httptest.NewRecorder()
	RecoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") })).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "</html>") {
		t.Fatalf("anonymous error should be plain text, got %d %s", rec.Code, truncate(rec.Body.String()))
	}
}

func TestNotFoundHandlerAPIEnabledJSON(t *testing.T) {
	withScratchErrorsConfig(t, "on")
	mux := http.NewServeMux()
	srv := httptest.NewServer(NotFoundHandler(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/whatever")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "does not exist") {
		t.Fatalf("expected api-enabled 404 message, got %s", body)
	}
}

func TestNotFoundHandlerAPIDisabledJSON(t *testing.T) {
	withScratchErrorsConfig(t, "off")
	mux := http.NewServeMux()
	srv := httptest.NewServer(NotFoundHandler(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/whatever")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "API access is disabled") {
		t.Fatalf("expected api-disabled 404 message, got %s", body)
	}
}

func TestRecoverMiddlewareCatchesPanic(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	srv := httptest.NewServer(loggedIn(RecoverMiddleware(next)))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	got := string(body)
	for _, want := range []string{"boom", "500", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected panic message rendered, got %s", truncate(got))
		}
	}
}

func TestCSRFErrorHandlerReturnsJSON(t *testing.T) {
	srv := httptest.NewServer(CSRFErrorHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"error":"CSRF error"`) {
		t.Fatalf("expected CSRF error JSON, got %s", body)
	}
}
