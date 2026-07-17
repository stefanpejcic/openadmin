package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestBasicAuthMiddlewareDisabledPassesThrough(t *testing.T) {
	handler := BasicAuthMiddleware(false, "admin", "secret")(newOKHandler())

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 when basic auth is disabled, got %d", rr.Code)
	}
}

func TestBasicAuthMiddlewareRejectsMissingCredentials(t *testing.T) {
	handler := BasicAuthMiddleware(true, "admin", "secret")(newOKHandler())

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credentials, got %d", rr.Code)
	}
	if rr.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("expected a WWW-Authenticate challenge header")
	}
}

func TestBasicAuthMiddlewareRejectsWrongCredentials(t *testing.T) {
	handler := BasicAuthMiddleware(true, "admin", "secret")(newOKHandler())

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.SetBasicAuth("admin", "wrong-password")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong credentials, got %d", rr.Code)
	}
}

func TestBasicAuthMiddlewareAcceptsCorrectCredentials(t *testing.T) {
	handler := BasicAuthMiddleware(true, "admin", "secret")(newOKHandler())

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.SetBasicAuth("admin", "secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct credentials, got %d", rr.Code)
	}
}

func TestBasicAuthMiddlewareExemptsAPIAndWebhookPaths(t *testing.T) {
	handler := BasicAuthMiddleware(true, "admin", "secret")(newOKHandler())

	for _, path := range []string{"/api/whoami", "/send_email", "/imav/inbox"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected %s to be exempt from basic auth, got %d", path, rr.Code)
		}
	}
}
