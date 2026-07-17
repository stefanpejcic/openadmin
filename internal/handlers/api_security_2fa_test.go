package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newAPISecurity2FATestServer(t *testing.T, role string) (*httptest.Server, *admindb.DB, string) {
	t.Helper()
	db := newAPITestDB(t)

	hash, _ := auth.GeneratePasswordHash("pw123456")
	db.CreateUser("alice", hash, role)
	alice, err := db.UserByUsername("alice")
	if err != nil {
		t.Fatal(err)
	}

	apiAuth := &APIAuth{DB: db, SecretKey: "test-secret"}
	a := &APISecurity2FA{Auth: apiAuth}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/security/2fa", apiAuth.RequireAPIToken(a.ServeStatus))
	mux.HandleFunc("POST /api/security/2fa/enable", apiAuth.RequireAPIToken(a.HandleEnable))
	mux.HandleFunc("POST /api/security/2fa/disable", apiAuth.RequireAPIToken(a.HandleDisable))
	mux.HandleFunc("GET /api/security/passkeys", apiAuth.RequireAPIToken(a.ServePasskeys))
	mux.HandleFunc("POST /api/security/passkeys", apiAuth.RequireAPIToken(a.ServePasskeys))
	mux.HandleFunc("DELETE /api/security/passkeys", apiAuth.RequireAPIToken(a.ServePasskeys))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	token, err := createAPIToken(alice.Username, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	return srv, db, token
}

func apiJSONRequest(t *testing.T, method, url, token, body string) *http.Request {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// --- 2FA status ---

func TestAPISecurity2FAStatusGeneratesFreshSecretEachCall(t *testing.T) {
	srv, _, token := newAPISecurity2FATestServer(t, "user")

	req := apiJSONRequest(t, http.MethodGet, srv.URL+"/api/security/2fa", token, "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var body1 map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body1)
	resp.Body.Close()
	if body1["totp_enabled"] != false || body1["secret"] == "" || body1["secret"] == nil {
		t.Fatalf("expected a secret when 2FA is disabled, got %+v", body1)
	}
	if !strings.HasPrefix(body1["qr_data_uri"].(string), "data:image/png;base64,") {
		t.Fatalf("expected a QR data URI, got %+v", body1)
	}

	req2 := apiJSONRequest(t, http.MethodGet, srv.URL+"/api/security/2fa", token, "")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	var body2 map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&body2)
	resp2.Body.Close()

	// The API is stateless -- unlike the session-backed HTML enrollment
	// page, it must NOT reuse the same pending secret across requests.
	if body1["secret"] == body2["secret"] {
		t.Fatal("expected a fresh secret on each call, got the same one twice")
	}
}

func TestAPISecurity2FAStatusReportsEnabled(t *testing.T) {
	srv, db, token := newAPISecurity2FATestServer(t, "user")
	db.SetTOTP("alice", "SOMESECRET", true)

	req := apiJSONRequest(t, http.MethodGet, srv.URL+"/api/security/2fa", token, "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["totp_enabled"] != true {
		t.Fatalf("expected totp_enabled:true, got %+v", body)
	}
	if _, hasSecret := body["secret"]; hasSecret {
		t.Fatalf("expected no secret once 2FA is already enabled, got %+v", body)
	}
}

func TestAPISecurity2FAStatusUnknownUserIs404(t *testing.T) {
	srv, db, token := newAPISecurity2FATestServer(t, "user")
	db.DeleteUser("alice")

	req := apiJSONRequest(t, http.MethodGet, srv.URL+"/api/security/2fa", token, "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for a deleted user, got %d", resp.StatusCode)
	}
}

// --- 2FA enable ---

func TestAPISecurity2FAEnableWithCorrectCode(t *testing.T) {
	srv, db, token := newAPISecurity2FATestServer(t, "user")

	req := apiJSONRequest(t, http.MethodGet, srv.URL+"/api/security/2fa", token, "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()
	secret := status["secret"].(string)

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	enableReq := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/security/2fa/enable", token,
		`{"secret":"`+secret+`","code":"`+code+`"}`)
	enableResp, err := http.DefaultClient.Do(enableReq)
	if err != nil {
		t.Fatal(err)
	}
	defer enableResp.Body.Close()
	if enableResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(enableResp.Body)
		t.Fatalf("expected 200, got %d: %s", enableResp.StatusCode, body)
	}

	u, err := db.UserByUsername("alice")
	if err != nil {
		t.Fatal(err)
	}
	if !u.TOTPEnabled || u.TOTPSecret.String != secret {
		t.Fatalf("expected 2FA enabled with the given secret, got %+v", u)
	}
}

func TestAPISecurity2FAEnableWithWrongCodeIs400(t *testing.T) {
	srv, db, token := newAPISecurity2FATestServer(t, "user")

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/security/2fa/enable", token,
		`{"secret":"JBSWY3DPEHPK3PXP","code":"000000"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	u, _ := db.UserByUsername("alice")
	if u.TOTPEnabled {
		t.Fatal("expected 2FA to remain disabled")
	}
}

func TestAPISecurity2FAEnableMissingFieldsIs400(t *testing.T) {
	srv, _, token := newAPISecurity2FATestServer(t, "user")

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/security/2fa/enable", token, `{"secret":"","code":""}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPISecurity2FAEnableValidatesCodeBeforeUserLookup(t *testing.T) {
	// An invalid code must be reported even if the calling token's user has
	// been deleted in the meantime -- code verification happens before the
	// user row is fetched.
	srv, db, token := newAPISecurity2FATestServer(t, "user")
	db.DeleteUser("alice")

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/security/2fa/enable", token,
		`{"secret":"JBSWY3DPEHPK3PXP","code":"000000"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 (invalid code) rather than 404 (missing user), got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Invalid authentication code") {
		t.Fatalf("expected the invalid-code message, got %s", body)
	}
}

// --- 2FA disable ---

func TestAPISecurity2FADisableWithCorrectPassword(t *testing.T) {
	srv, db, token := newAPISecurity2FATestServer(t, "user")
	db.SetTOTP("alice", "SOMESECRET", true)

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/security/2fa/disable", token, `{"password":"pw123456"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	u, _ := db.UserByUsername("alice")
	if u.TOTPEnabled {
		t.Fatal("expected 2FA disabled")
	}
}

func TestAPISecurity2FADisableWithWrongPasswordIs400(t *testing.T) {
	srv, db, token := newAPISecurity2FATestServer(t, "user")
	db.SetTOTP("alice", "SOMESECRET", true)

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/security/2fa/disable", token, `{"password":"wrong"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	u, _ := db.UserByUsername("alice")
	if !u.TOTPEnabled {
		t.Fatal("expected 2FA to remain enabled after a failed disable attempt")
	}
}

// --- passkeys ---

func TestAPISecurityPasskeysListsOwnCredentials(t *testing.T) {
	srv, db, token := newAPISecurity2FATestServer(t, "user")
	alice, _ := db.UserByUsername("alice")
	db.CreateCredential(alice.ID, "cred-1", "pubkey", 0, "YubiKey")

	req := apiJSONRequest(t, http.MethodGet, srv.URL+"/api/security/passkeys", token, "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "YubiKey") {
		t.Fatalf("expected the passkey listed, got %s", body)
	}
}

func TestAPISecurityPasskeysRenameOwnCredential(t *testing.T) {
	srv, db, token := newAPISecurity2FATestServer(t, "user")
	alice, _ := db.UserByUsername("alice")
	credID, _ := db.CreateCredential(alice.ID, "cred-1", "pubkey", 0, "Old Name")

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/security/passkeys", token,
		`{"id":`+strconv.FormatInt(credID, 10)+`,"name":"New Name"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	creds, _ := db.CredentialsByUserID(alice.ID)
	if len(creds) != 1 || creds[0].Name.String != "New Name" {
		t.Fatalf("expected the credential renamed, got %+v", creds)
	}
}

func TestAPISecurityPasskeysCannotRenameAnotherUsersCredential(t *testing.T) {
	srv, db, token := newAPISecurity2FATestServer(t, "user")
	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("bob", hash, "user")
	bob, _ := db.UserByUsername("bob")
	credID, _ := db.CreateCredential(bob.ID, "bobs-cred", "pubkey", 0, "Bob's Key")

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/security/passkeys", token,
		`{"id":`+strconv.FormatInt(credID, 10)+`,"name":"Hijacked"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for another user's credential, got %d", resp.StatusCode)
	}

	creds, _ := db.CredentialsByUserID(bob.ID)
	if creds[0].Name.String != "Bob's Key" {
		t.Fatalf("expected bob's credential untouched, got %+v", creds)
	}
}

func TestAPISecurityPasskeysDeleteOwnCredential(t *testing.T) {
	srv, db, token := newAPISecurity2FATestServer(t, "user")
	alice, _ := db.UserByUsername("alice")
	credID, _ := db.CreateCredential(alice.ID, "cred-1", "pubkey", 0, "To Delete")

	req := apiJSONRequest(t, http.MethodDelete, srv.URL+"/api/security/passkeys", token,
		`{"id":`+strconv.FormatInt(credID, 10)+`}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	creds, _ := db.CredentialsByUserID(alice.ID)
	if len(creds) != 0 {
		t.Fatalf("expected the credential deleted, got %+v", creds)
	}
}

func TestAPISecurityPasskeysRenameEmptyNameIs400(t *testing.T) {
	srv, db, token := newAPISecurity2FATestServer(t, "user")
	alice, _ := db.UserByUsername("alice")
	credID, _ := db.CreateCredential(alice.ID, "cred-1", "pubkey", 0, "Old Name")

	req := apiJSONRequest(t, http.MethodPost, srv.URL+"/api/security/passkeys", token,
		`{"id":`+strconv.FormatInt(credID, 10)+`,"name":"   "}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPISecurityPasskeysInvalidIDIs404(t *testing.T) {
	srv, _, token := newAPISecurity2FATestServer(t, "user")

	req := apiJSONRequest(t, http.MethodDelete, srv.URL+"/api/security/passkeys", token, `{"id":99999}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
