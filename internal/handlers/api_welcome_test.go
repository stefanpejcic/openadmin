package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"openadmin/internal/auth"
)

func newAPIWelcomeTestServer(t *testing.T) (*APIWelcome, *httptest.Server, *http.Client) {
	t.Helper()
	db := newAPITestDB(t)
	hash, err := auth.GeneratePasswordHash("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser("alice", hash, "admin"); err != nil {
		t.Fatal(err)
	}

	w := &APIWelcome{DB: db, SecretKey: "test-secret", Limiter: auth.NewPerIPLimiter(1000, 1000)}
	a := &APIAuth{DB: db, SecretKey: "test-secret"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/", w.ServeWelcome)
	mux.HandleFunc("POST /api/", w.ServeWelcome)
	mux.HandleFunc("GET /api/whoami", a.RequireAPIToken(w.ServeWhoami))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return w, srv, srv.Client()
}

func TestServeWelcomeGetReturnsStatus(t *testing.T) {
	_, srv, client := newAPIWelcomeTestServer(t)
	resp, err := client.Get(srv.URL + "/api/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["message"] != "API is working!" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestServeWelcomePostValidCredentialsReturnsToken(t *testing.T) {
	_, srv, client := newAPIWelcomeTestServer(t)
	resp, err := client.Post(srv.URL+"/api/", "application/json",
		strings.NewReader(`{"username":"alice","password":"correct-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["access_token"] == "" {
		t.Fatalf("expected an access_token, got %v", body)
	}

	claims := &apiJWTClaims{}
	if _, err := jwt.ParseWithClaims(body["access_token"], claims, func(t *jwt.Token) (interface{}, error) {
		return []byte("test-secret"), nil
	}); err != nil {
		t.Fatalf("issued token did not parse: %v", err)
	}
	if claims.Subject != "alice" {
		t.Fatalf("expected token subject alice, got %q", claims.Subject)
	}
}

func TestServeWelcomePostWrongPasswordReturns401(t *testing.T) {
	_, srv, client := newAPIWelcomeTestServer(t)
	resp, err := client.Post(srv.URL+"/api/", "application/json",
		strings.NewReader(`{"username":"alice","password":"wrong"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestServeWelcomePostUnknownUserReturns401(t *testing.T) {
	_, srv, client := newAPIWelcomeTestServer(t)
	resp, err := client.Post(srv.URL+"/api/", "application/json",
		strings.NewReader(`{"username":"nobody","password":"whatever"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestServeWelcomePostMalformedJSONReturns400(t *testing.T) {
	_, srv, client := newAPIWelcomeTestServer(t)
	resp, err := client.Post(srv.URL+"/api/", "application/json", strings.NewReader(`not json`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServeWelcomePostRateLimited(t *testing.T) {
	db := newAPITestDB(t)
	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("alice", hash, "admin")

	w := &APIWelcome{DB: db, SecretKey: "test-secret", Limiter: auth.NewPerIPLimiter(1, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/", w.ServeWelcome)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := srv.Client()
	body := `{"username":"alice","password":"pw"}`
	resp1, err := client.Post(srv.URL+"/api/", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()

	resp2, err := client.Post(srv.URL+"/api/", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected second immediate request to be rate-limited (429), got %d", resp2.StatusCode)
	}
}

func TestServeWhoamiReturnsUsernameFromToken(t *testing.T) {
	_, srv, client := newAPIWelcomeTestServer(t)
	token, err := createAPIToken("alice", "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", srv.URL+"/api/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out map[string]string
	json.NewDecoder(resp.Body).Decode(&out)
	if out["logged_in_as"] != "alice" {
		t.Fatalf("expected logged_in_as alice, got %v", out)
	}
}

func TestServeWhoamiWithoutTokenReturns401(t *testing.T) {
	_, srv, client := newAPIWelcomeTestServer(t)
	resp, err := client.Get(srv.URL + "/api/whoami")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
