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

func TestResellerLoginIssuesWorkingLinkWithoutPassword(t *testing.T) {
	a, srv, client := newAPILoginTestServer(t)
	hash, _ := auth.GeneratePasswordHash("whatever1")
	a.DB.CreateUser("res1", hash, "reseller")
	a.DB.CreateUser("res2", hash, "reseller")
	a.DB.SetActive("res2", false)

	login := func(name string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/settings/resellers/"+name+"/login", nil)
		req.SetPathValue("username", name)
		rec := httptest.NewRecorder()
		a.ServeResellerLogin(rec, req)
		return rec
	}

	rec := login("res1")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		LoginPath string `json:"login_path"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	resp, err := client.Get(srv.URL + body.LoginPath)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/dashboard" {
		t.Fatalf("expected a redirect to /dashboard, got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	if rec := login("alice"); rec.Code != http.StatusNotFound {
		t.Fatalf("admins must not get a link this way, got %d", rec.Code)
	}
	if rec := login("res2"); rec.Code != http.StatusForbidden {
		t.Fatalf("suspended reseller should get 403, got %d", rec.Code)
	}
}

func TestAPITokenLoginAndTokensRefusedForSuspendedUser(t *testing.T) {
	db := newAPITestDB(t)
	hash, _ := auth.GeneratePasswordHash("pw123456")
	db.CreateUser("res1", hash, "reseller")
	a := &APIAuth{DB: db, SecretKey: "test-secret"}
	token, _ := createAPIToken("res1", "test-secret")
	db.SetActive("res1", false)

	rec := httptest.NewRecorder()
	(&APIWelcome{DB: db, SecretKey: "test-secret", Limiter: auth.NewPerIPLimiter(1000, 1000)}).ServeWelcome(rec, httptest.NewRequest(http.MethodPost, "/api/", strings.NewReader(`{"username":"res1","password":"pw123456"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("login: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	ran := false
	next := func(http.ResponseWriter, *http.Request) { ran = true }
	for name, h := range map[string]http.HandlerFunc{
		"token":    a.RequireAPIToken(next),
		"reseller": a.RequireAPIAdminOrReseller(next),
		"owner":    a.RequireAPIOwnerOrAdmin("username", next),
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h(rec, req)
		if ran || rec.Code < 400 {
			t.Fatalf("%s: a suspended user's old token must be refused, got %d", name, rec.Code)
		}
	}
}
