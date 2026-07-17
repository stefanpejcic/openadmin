package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/config"
)

func newSlaveTestServer(t *testing.T, s *Slave) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	origAdminConfig := config.AdminConfigPath
	config.AdminConfigPath = filepath.Join(dir, "admin.ini")
	t.Cleanup(func() { config.AdminConfigPath = origAdminConfig })

	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("caller", hash, "admin")
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	s.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /server/node", s.ServeNode)
	mux.HandleFunc("POST /server/node", s.ServeNode)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
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
	return srv, client
}

func TestSlaveValidateSSHConnectionFailureForBogusHost(t *testing.T) {
	valid, errMsg := slaveValidateSSHConnection("nonexistent.invalid.host.example", "/nonexistent/key")
	if valid {
		t.Fatal("expected validation to fail for a bogus host/key")
	}
	if errMsg == "" {
		t.Fatal("expected a non-empty error message")
	}
}

func TestServeNodeGETEmptyConfig(t *testing.T) {
	s := &Slave{}
	srv, client := newSlaveTestServer(t, s)

	resp, err := client.Get(srv.URL + "/server/node")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{`name="default_node"`, "Default Node Server", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeNodeGETJSON(t *testing.T) {
	s := &Slave{}
	srv, client := newSlaveTestServer(t, s)

	data := config.Load(config.AdminConfigPath)
	data.Set("CLUSTERING", "default_node", `"192.0.2.10"`)
	if err := config.Save(config.AdminConfigPath, data); err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get(srv.URL + "/server/node?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	// Quotes must be stripped in the JSON output (unlike the HTML template,
	// which gets the raw value -- see the comment in slave.go).
	if !strings.Contains(string(body), `"default_node":"192.0.2.10"`) {
		t.Fatalf("expected quote-stripped default_node in JSON, got %s", truncate(string(body)))
	}
}

func TestServeNodeGETHTMLKeepsRawQuotedValue(t *testing.T) {
	s := &Slave{}
	srv, client := newSlaveTestServer(t, s)

	data := config.Load(config.AdminConfigPath)
	data.Set("CLUSTERING", "default_node", `"192.0.2.10"`)
	if err := config.Save(config.AdminConfigPath, data); err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get(srv.URL + "/server/node")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// The HTML template gets the RAW value (still quoted), matching the
	// original's inconsistency between the JSON and HTML response paths.
	if !strings.Contains(string(body), `&#34;192.0.2.10&#34;`) {
		t.Fatalf("expected the raw quoted value escaped into the HTML form, got %s", truncate(string(body)))
	}
}

func TestServeNodePOSTSavesWithoutSSHFieldsProvided(t *testing.T) {
	s := &Slave{}
	srv, client := newSlaveTestServer(t, s)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/server/node", url.Values{
		"default_node": {"node1.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "edited successfully") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	data := config.Load(config.AdminConfigPath)
	if got := data.Get("CLUSTERING", "default_node", ""); got != "node1.example.com" {
		t.Fatalf("expected default_node to be saved, got %q", got)
	}
	// default_ssh_key_path was never submitted, so it must not be set.
	if _, ok := data["CLUSTERING"]["default_ssh_key_path"]; ok {
		t.Fatal("expected default_ssh_key_path to remain unset")
	}
}

func TestServeNodePOSTSkipsSaveOnSSHValidationFailure(t *testing.T) {
	s := &Slave{}
	srv, client := newSlaveTestServer(t, s)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/server/node", url.Values{
		"default_node":         {"nonexistent.invalid.host.example"},
		"default_ssh_key_path": {"/nonexistent/key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "SSH validation failed") {
		t.Fatalf("expected an SSH-validation-failed flash, got %s", truncate(string(body)))
	}

	data := config.Load(config.AdminConfigPath)
	if _, ok := data["CLUSTERING"]; ok {
		t.Fatal("expected nothing saved when SSH validation fails")
	}
}

func TestServeNodePOSTOnlyUpdatesSubmittedFields(t *testing.T) {
	s := &Slave{}
	srv, client := newSlaveTestServer(t, s)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	data := config.Load(config.AdminConfigPath)
	data.Set("CLUSTERING", "default_node", "old-node")
	data.Set("CLUSTERING", "default_ssh_key_path", "/old/key")
	if err := config.Save(config.AdminConfigPath, data); err != nil {
		t.Fatal(err)
	}

	resp, err := client.PostForm(srv.URL+"/server/node", url.Values{
		"default_ssh_key_path": {"/new/key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	data = config.Load(config.AdminConfigPath)
	if got := data.Get("CLUSTERING", "default_node", ""); got != "old-node" {
		t.Fatalf("expected default_node left untouched, got %q", got)
	}
	if got := data.Get("CLUSTERING", "default_ssh_key_path", ""); got != "/new/key" {
		t.Fatalf("expected default_ssh_key_path updated, got %q", got)
	}
}
