package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newDNSClusterTestServer(t *testing.T, d *DNSCluster) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	origConfigPath := DNSClusterConfigPath
	DNSClusterConfigPath = filepath.Join(dir, "named.conf.options")
	t.Cleanup(func() { DNSClusterConfigPath = origConfigPath })

	origRestart := dnsRestartServiceRun
	dnsRestartServiceRun = func() {}
	t.Cleanup(func() { dnsRestartServiceRun = origRestart })

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
	d.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /domains/dns-cluster", d.ServeDNSCluster)
	mux.HandleFunc("POST /domains/dns-cluster", d.ServeDNSCluster)
	mux.HandleFunc("GET /domains/dns-cluster/{ip}", d.ServeDNSClusterInfo)
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

const testNamedConfOptionsDisabled = `options {
	// allow-transfer {1.2.3.4;};
	// also-notify {1.2.3.4;};
};
`

const testNamedConfOptionsEnabled = `options {
	allow-transfer {1.2.3.4;5.6.7.8;};
	also-notify {1.2.3.4;5.6.7.8;};
};
`

func TestDNSDirectiveUncommented(t *testing.T) {
	if dnsDirectiveUncommented(testNamedConfOptionsDisabled, "allow-transfer") {
		t.Fatal("expected commented directive to be treated as not-uncommented")
	}
	if !dnsDirectiveUncommented(testNamedConfOptionsEnabled, "allow-transfer") {
		t.Fatal("expected active directive to be treated as uncommented")
	}
}

func TestExtractDNSClusterConfigParsesBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "named.conf.options")
	os.WriteFile(path, []byte(testNamedConfOptionsEnabled), 0644)

	got, err := extractDNSClusterConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled {
		t.Fatal("expected enabled=true for uncommented directives")
	}
	if len(got.AllowTransfer) != 2 || got.AllowTransfer[0] != "1.2.3.4" || got.AllowTransfer[1] != "5.6.7.8" {
		t.Fatalf("unexpected allow_transfer: %+v", got.AllowTransfer)
	}
	if len(got.AlsoNotify) != 2 {
		t.Fatalf("unexpected also_notify: %+v", got.AlsoNotify)
	}
}

func TestExtractDNSClusterConfigDisabledWhenCommented(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "named.conf.options")
	os.WriteFile(path, []byte(testNamedConfOptionsDisabled), 0644)

	got, err := extractDNSClusterConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Fatal("expected enabled=false when directives are commented out")
	}
}

func TestAddIPToConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "named.conf.options")
	os.WriteFile(path, []byte(testNamedConfOptionsEnabled), 0644)

	if err := addIPToConfig(path, "9.9.9.9"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	content := string(raw)
	if !strings.Contains(content, "allow-transfer {1.2.3.4;5.6.7.8;9.9.9.9;};") {
		t.Fatalf("expected new IP appended to allow-transfer, got:\n%s", content)
	}
	if !strings.Contains(content, "also-notify {1.2.3.4;5.6.7.8;9.9.9.9;};") {
		t.Fatalf("expected new IP appended to also-notify, got:\n%s", content)
	}
}

func TestAddIPToConfigNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "named.conf.options")
	os.WriteFile(path, []byte(testNamedConfOptionsEnabled), 0644)

	if err := addIPToConfig(path, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), "1.2.3.4") != 2 { // once in each block, no duplicate
		t.Fatalf("expected no duplicate entries, got:\n%s", string(raw))
	}
}

func TestRemoveIPFromConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "named.conf.options")
	os.WriteFile(path, []byte(testNamedConfOptionsEnabled), 0644)

	if err := removeIPFromConfig(path, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	content := string(raw)
	if strings.Contains(content, "1.2.3.4") {
		t.Fatalf("expected 1.2.3.4 removed, got:\n%s", content)
	}
	if !strings.Contains(content, "allow-transfer {5.6.7.8;};") {
		t.Fatalf("expected remaining entry preserved, got:\n%s", content)
	}
}

func TestUpdateDNSClusterConfigFileEnableAndDisable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "named.conf.options")
	os.WriteFile(path, []byte(testNamedConfOptionsDisabled), 0644)

	if err := updateDNSClusterConfigFile(path, true); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "// allow-transfer") {
		t.Fatalf("expected allow-transfer uncommented, got:\n%s", string(raw))
	}

	if err := updateDNSClusterConfigFile(path, false); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "// allow-transfer") {
		t.Fatalf("expected allow-transfer re-commented, got:\n%s", string(raw))
	}
}

func TestServeDNSClusterGETJSON(t *testing.T) {
	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)
	os.WriteFile(DNSClusterConfigPath, []byte(testNamedConfOptionsEnabled), 0644)

	resp, err := client.Get(srv.URL + "/domains/dns-cluster?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), `"enabled":true`) {
		t.Fatalf("expected enabled:true, got %s", truncate(string(body)))
	}
}

func TestServeDNSClusterRendersHTML(t *testing.T) {
	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)
	os.WriteFile(DNSClusterConfigPath, []byte(testNamedConfOptionsEnabled), 0644)

	resp, err := client.Get(srv.URL + "/domains/dns-cluster")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"DNS Cluster Management", "Disable DNS Clustering", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeDNSClusterPOSTEnableDisable(t *testing.T) {
	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)
	os.WriteFile(DNSClusterConfigPath, []byte(testNamedConfOptionsDisabled), 0644)

	resp, err := client.PostForm(srv.URL+"/domains/dns-cluster", url.Values{"action": {"enable"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "DNS cluster enabled successfully") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
}

func TestServeDNSClusterPOSTCreateInvalidFormat(t *testing.T) {
	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)
	os.WriteFile(DNSClusterConfigPath, []byte(testNamedConfOptionsEnabled), 0644)

	resp, err := client.PostForm(srv.URL+"/domains/dns-cluster", url.Values{
		"action": {"create"}, "ip": {"not-an-ip"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Invalid IP address format") {
		t.Fatalf("expected invalid-format flash, got %s", truncate(string(body)))
	}
}

func TestServeDNSClusterPOSTCreateIPv6FallsThroughInsteadOfCrashing(t *testing.T) {
	// Regression test: a bare `return` here would abandon the request with
	// no response (a 500). The handler must fall through and still render
	// the page normally.
	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)
	os.WriteFile(DNSClusterConfigPath, []byte(testNamedConfOptionsEnabled), 0644)

	resp, err := client.PostForm(srv.URL+"/domains/dns-cluster", url.Values{
		"action": {"create"}, "ip": {"2001:db8::1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (not a crash), got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Only IPv4 addresses are currently supported") {
		t.Fatalf("expected the IPv4-only flash, got %s", truncate(string(body)))
	}
	// And the page must still show the normal form content, proving it
	// fell through to the real render rather than returning early.
	if !strings.Contains(string(body), `name="ip"`) {
		t.Fatalf("expected the page to render normally after the flash, got %s", truncate(string(body)))
	}
}

func TestServeDNSClusterPOSTCreateAlreadyExists(t *testing.T) {
	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)
	os.WriteFile(DNSClusterConfigPath, []byte(testNamedConfOptionsEnabled), 0644)

	resp, err := client.PostForm(srv.URL+"/domains/dns-cluster", url.Values{
		"action": {"create"}, "ip": {"1.2.3.4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "already exists in configuration") {
		t.Fatalf("expected already-exists flash, got %s", truncate(string(body)))
	}
}

func TestServeDNSClusterPOSTCreateUnreachableSlave(t *testing.T) {
	origRun := dnsRNDCCommandRun
	dnsRNDCCommandRun = func(slaveIP string, args ...string) (bool, string) { return false, "connection refused" }
	t.Cleanup(func() { dnsRNDCCommandRun = origRun })

	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)
	os.WriteFile(DNSClusterConfigPath, []byte(testNamedConfOptionsEnabled), 0644)

	resp, err := client.PostForm(srv.URL+"/domains/dns-cluster", url.Values{
		"action": {"create"}, "ip": {"9.9.9.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Cannot reach 9.9.9.9 via rndc") {
		t.Fatalf("expected unreachable-slave flash, got %s", truncate(string(body)))
	}
}

func TestServeDNSClusterPOSTCreateSuccessSpawnsSync(t *testing.T) {
	origRNDC := dnsRNDCCommandRun
	dnsRNDCCommandRun = func(slaveIP string, args ...string) (bool, string) { return true, "number of zones: 3" }
	t.Cleanup(func() { dnsRNDCCommandRun = origRNDC })

	origDomainsAll := dnsDomainsAllRun
	syncCalled := make(chan struct{}, 1)
	dnsDomainsAllRun = func() (string, error) {
		select {
		case syncCalled <- struct{}{}:
		default:
		}
		return "", nil
	}
	t.Cleanup(func() { dnsDomainsAllRun = origDomainsAll })

	d := &DNSCluster{PublicIP: "10.0.0.1"}
	srv, client := newDNSClusterTestServer(t, d)
	os.WriteFile(DNSClusterConfigPath, []byte(testNamedConfOptionsEnabled), 0644)

	resp, err := client.PostForm(srv.URL+"/domains/dns-cluster", url.Values{
		"action": {"create"}, "ip": {"9.9.9.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "added to DNS cluster successfully") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	select {
	case <-syncCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the background zone-sync goroutine to run")
	}
}

func TestServeDNSClusterPOSTDeleteNotFound(t *testing.T) {
	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)
	os.WriteFile(DNSClusterConfigPath, []byte(testNamedConfOptionsEnabled), 0644)

	resp, err := client.PostForm(srv.URL+"/domains/dns-cluster", url.Values{
		"action": {"delete"}, "ip": {"9.9.9.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "not found in DNS cluster configuration") {
		t.Fatalf("expected not-found flash, got %s", truncate(string(body)))
	}
}

func TestServeDNSClusterPOSTDeleteSuccess(t *testing.T) {
	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)
	os.WriteFile(DNSClusterConfigPath, []byte(testNamedConfOptionsEnabled), 0644)

	resp, err := client.PostForm(srv.URL+"/domains/dns-cluster", url.Values{
		"action": {"delete"}, "ip": {"1.2.3.4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "removed from DNS cluster successfully") {
		t.Fatalf("expected removed flash, got %s", truncate(string(body)))
	}

	raw, _ := os.ReadFile(DNSClusterConfigPath)
	if strings.Contains(string(raw), "1.2.3.4") {
		t.Fatalf("expected 1.2.3.4 actually removed from disk, got:\n%s", string(raw))
	}
}

func TestServeDNSClusterInfoRNDCSuccess(t *testing.T) {
	origRun := dnsRNDCCommandRun
	dnsRNDCCommandRun = func(slaveIP string, args ...string) (bool, string) {
		return true, "number of zones: 5"
	}
	t.Cleanup(func() { dnsRNDCCommandRun = origRun })

	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)

	resp, err := client.Get(srv.URL + "/domains/dns-cluster/9.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"method":"rndc"`) || !strings.Contains(string(body), `"status":"success"`) {
		t.Fatalf("expected rndc success, got %s", truncate(string(body)))
	}
}

func TestServeDNSClusterInfoFallsBackToSSH(t *testing.T) {
	origRNDC := dnsRNDCCommandRun
	dnsRNDCCommandRun = func(slaveIP string, args ...string) (bool, string) { return false, "" }
	t.Cleanup(func() { dnsRNDCCommandRun = origRNDC })

	origSSH := dnsSSHRun
	dnsSSHRun = func(host string, timeout time.Duration, remoteCmd string) (int, string, string, bool, error) {
		return 0, "Linux myhost 5.x", "", false, nil
	}
	t.Cleanup(func() { dnsSSHRun = origSSH })

	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)

	resp, err := client.Get(srv.URL + "/domains/dns-cluster/9.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"method":"ssh"`) || !strings.Contains(string(body), `"status":"success"`) {
		t.Fatalf("expected ssh success, got %s", truncate(string(body)))
	}
}

func TestServeDNSClusterInfoTimeout(t *testing.T) {
	origRNDC := dnsRNDCCommandRun
	dnsRNDCCommandRun = func(slaveIP string, args ...string) (bool, string) { return false, "" }
	t.Cleanup(func() { dnsRNDCCommandRun = origRNDC })

	origSSH := dnsSSHRun
	dnsSSHRun = func(host string, timeout time.Duration, remoteCmd string) (int, string, string, bool, error) {
		return 0, "", "", true, context.DeadlineExceeded
	}
	t.Cleanup(func() { dnsSSHRun = origSSH })

	d := &DNSCluster{}
	srv, client := newDNSClusterTestServer(t, d)

	resp, err := client.Get(srv.URL + "/domains/dns-cluster/9.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"timeout"`) {
		t.Fatalf("expected timeout status, got %s", truncate(string(body)))
	}
}
