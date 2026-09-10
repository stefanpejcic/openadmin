package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"openadmin/internal/auth"
)

func newAPILoginTestServer(t *testing.T) (*APILogin, *httptest.Server, *http.Client) {
	t.Helper()
	db := newAPITestDB(t)
	hash, err := auth.GeneratePasswordHash("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser("alice", hash, "admin"); err != nil {
		t.Fatal(err)
	}

	a := &APILogin{DB: db, Sessions: auth.NewManager("test-secret", false), Limiter: auth.NewPerIPLimiter(1000, 1000)}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", a.HandleAPILogin)
	mux.HandleFunc("GET /login/sso/{token}", a.HandleSSOLogin)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return a, srv, &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestAPILoginWrongPasswordReturns401(t *testing.T) {
	_, srv, client := newAPILoginTestServer(t)
	resp, err := client.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"alice","password":"wrong"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAPILoginValidCredentialsIssuesRedeemableToken(t *testing.T) {
	_, srv, client := newAPILoginTestServer(t)

	resp, err := client.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"alice","password":"correct-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatalf("expected a token, got %v", body)
	}

	redeem, err := client.Get(srv.URL + "/login/sso/" + token)
	if err != nil {
		t.Fatal(err)
	}
	defer redeem.Body.Close()
	if redeem.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", redeem.StatusCode)
	}
	if loc := redeem.Header.Get("Location"); loc != "/dashboard" {
		t.Fatalf("expected redirect to /dashboard, got %q", loc)
	}
	var sawSessionCookie bool
	for _, c := range redeem.Cookies() {
		if c.Name == auth.SessionCookieName {
			sawSessionCookie = true
		}
	}
	if !sawSessionCookie {
		t.Fatalf("expected session cookie to be set after SSO redemption")
	}

	// The token is single-use: redeeming it again must fail.
	replay, err := client.Get(srv.URL + "/login/sso/" + token)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Body.Close()
	if replay.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected replay to be rejected with 401, got %d", replay.StatusCode)
	}
}

func TestSSOLoginUnknownTokenReturns401(t *testing.T) {
	_, srv, client := newAPILoginTestServer(t)
	resp, err := client.Get(srv.URL + "/login/sso/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
