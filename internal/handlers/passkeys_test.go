package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newPasskeysTestServer(t *testing.T) (*httptest.Server, *http.Client, *admindb.DB, *admindb.User) {
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

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("alice", hash, "user")
	db.CreateUser("bob", hash, "user")
	alice, err := db.UserByUsername("alice")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	pk := &Passkeys{DB: db, Sessions: sessions}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /security/passkeys", pk.ServeSettings)
	mux.HandleFunc("POST /security/passkeys/rename", pk.HandleRename)
	mux.HandleFunc("POST /security/passkeys/delete", pk.HandleDelete)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, alice, "203.0.113.1")
	})

	handler := auth.WithUserLoader(sessions, db)(mux)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	if _, err := client.Get(srv.URL + "/login-as"); err != nil {
		t.Fatal(err)
	}
	return srv, client, db, alice
}

func TestPasskeysSettingsListsOwnCredentials(t *testing.T) {
	srv, client, db, alice := newPasskeysTestServer(t)
	credID, err := db.CreateCredential(alice.ID, "cred-1", "pubkey", 0, "YubiKey")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get(srv.URL + "/security/passkeys")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "YubiKey") {
		t.Fatalf("expected the passkey name in the page, got %s", truncate(string(body)))
	}
	_ = credID
}

func TestPasskeysRenameOwnCredential(t *testing.T) {
	srv, client, db, alice := newPasskeysTestServer(t)
	credID, err := db.CreateCredential(alice.ID, "cred-1", "pubkey", 0, "Old Name")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.PostForm(srv.URL+"/security/passkeys/rename", url.Values{
		"id": {strconv.FormatInt(credID, 10)}, "name": {"New Name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Passkey renamed.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	cred, err := db.CredentialByCredentialID("cred-1")
	if err != nil {
		t.Fatal(err)
	}
	if cred.Name.String != "New Name" {
		t.Fatalf("expected renamed credential, got %+v", cred)
	}
}

func TestPasskeysCannotRenameAnotherUsersCredential(t *testing.T) {
	srv, client, db, _ := newPasskeysTestServer(t)
	bob, err := db.UserByUsername("bob")
	if err != nil {
		t.Fatal(err)
	}
	credID, err := db.CreateCredential(bob.ID, "bobs-cred", "pubkey", 0, "Bob's Key")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.PostForm(srv.URL+"/security/passkeys/rename", url.Values{
		"id": {strconv.FormatInt(credID, 10)}, "name": {"Hijacked"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid passkey") {
		t.Fatalf("expected an ownership-denied flash, got %s", truncate(string(body)))
	}

	cred, err := db.CredentialByCredentialID("bobs-cred")
	if err != nil {
		t.Fatal(err)
	}
	if cred.Name.String != "Bob's Key" {
		t.Fatal("expected bob's credential to be untouched by alice's rename attempt")
	}
}

func TestPasskeysDeleteOwnCredential(t *testing.T) {
	srv, client, db, alice := newPasskeysTestServer(t)
	credID, err := db.CreateCredential(alice.ID, "cred-1", "pubkey", 0, "To Delete")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.PostForm(srv.URL+"/security/passkeys/delete", url.Values{
		"id": {strconv.FormatInt(credID, 10)},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Passkey removed.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	if _, err := db.CredentialByCredentialID("cred-1"); err != admindb.ErrNotFound {
		t.Fatalf("expected credential to be deleted, got err=%v", err)
	}
}

func TestPasskeysCannotDeleteAnotherUsersCredential(t *testing.T) {
	srv, client, db, _ := newPasskeysTestServer(t)
	bob, err := db.UserByUsername("bob")
	if err != nil {
		t.Fatal(err)
	}
	credID, err := db.CreateCredential(bob.ID, "bobs-cred", "pubkey", 0, "Bob's Key")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.PostForm(srv.URL+"/security/passkeys/delete", url.Values{
		"id": {strconv.FormatInt(credID, 10)},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	if _, err := db.CredentialByCredentialID("bobs-cred"); err != nil {
		t.Fatal("expected bob's credential to survive alice's delete attempt")
	}
}
