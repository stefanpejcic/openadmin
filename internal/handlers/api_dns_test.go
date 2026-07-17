package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newAPIDNSTestServer(t *testing.T, a *APIDNS) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /domains/{domain_name}/dns", a.ServeDomainDNSZone)
	mux.HandleFunc("POST /domains/{domain_name}/dns", a.ServeDomainDNSZone)
	mux.HandleFunc("GET /dns/cluster", a.ServeDNSCluster)
	mux.HandleFunc("POST /dns/cluster", a.ServeDNSCluster)
	mux.HandleFunc("GET /dns/cluster/{ip}", a.ServeDNSClusterNodeInfo)
	mux.HandleFunc("GET /dns/zone-templates", a.ServeDNSZoneTemplates)
	mux.HandleFunc("POST /dns/zone-templates", a.ServeDNSZoneTemplates)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

// --- GET/POST /domains/{domain_name}/dns ---

func TestAPIDomainDNSZoneInvalidDomainReturns400(t *testing.T) {
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/not-a-domain/dns")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid domain name.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainDNSZoneGetMissingFileReturns404(t *testing.T) {
	withScratchBindZonesDir(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/dns")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Zone file for example.com not found") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainDNSZoneGetReturnsExistingContent(t *testing.T) {
	zonesDir, _ := withScratchBindZonesDir(t)
	os.WriteFile(filepath.Join(zonesDir, "example.com.zone"), []byte("$TTL 3600\n"), 0644)

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/dns")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["domain"] != "example.com" || decoded["content"] != "$TTL 3600\n" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainDNSZonePostRequiresJSON(t *testing.T) {
	withScratchBindZonesDir(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/dns", "application/x-www-form-urlencoded", strings.NewReader("content=x"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid JSON format") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainDNSZonePostRequiresContent(t *testing.T) {
	withScratchBindZonesDir(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/dns", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "content is required") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainDNSZonePostSuccessWritesAndCleansBackup(t *testing.T) {
	zonesDir, backupDir := withScratchBindZonesDir(t)
	stubDNSZoneValidate(t, 0, "", nil)

	zonePath := filepath.Join(zonesDir, "example.com.zone")
	os.WriteFile(zonePath, []byte("old content\n"), 0644)

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/dns", "application/json", strings.NewReader(`{"content":"new content\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "saved successfully and DNS service reloaded.") {
		t.Fatalf("unexpected body: %s", body)
	}

	written, err := os.ReadFile(zonePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != "new content\n" {
		t.Fatalf("expected zone file updated, got %q", written)
	}
	leftovers, _ := filepath.Glob(filepath.Join(backupDir, "example.com.zone.backup_*"))
	if len(leftovers) != 0 {
		t.Fatalf("expected backup cleaned up, found %v", leftovers)
	}
}

func TestAPIDomainDNSZonePostValidationFailureReverts(t *testing.T) {
	zonesDir, _ := withScratchBindZonesDir(t)
	stubDNSZoneValidate(t, 1, "zone example.com/IN: has no NS records", nil)

	zonePath := filepath.Join(zonesDir, "example.com.zone")
	os.WriteFile(zonePath, []byte("original content\n"), 0644)

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/dns", "application/json", strings.NewReader(`{"content":"broken content\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Zone file validation failed. Changes reverted. Error:") {
		t.Fatalf("unexpected body: %s", body)
	}

	reverted, err := os.ReadFile(zonePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(reverted) != "original content\n" {
		t.Fatalf("expected file reverted, got %q", reverted)
	}
}

func TestAPIDomainDNSZonePostWriteFailureReturns500JSON(t *testing.T) {
	// Point BindZonesDir at a path component that can't be created (a file,
	// not a directory), so os.WriteFile fails -- this exercises the
	// try/except Exception -> {"success": false, "error": ...}, 500 path.
	dir := t.TempDir()
	blockerFile := filepath.Join(dir, "not-a-dir")
	os.WriteFile(blockerFile, []byte("x"), 0644)

	origZones := BindZonesDir
	BindZonesDir = blockerFile
	t.Cleanup(func() { BindZonesDir = origZones })

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/dns", "application/json", strings.NewReader(`{"content":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["success"] != false {
		t.Fatalf("expected a JSON success:false error body, got %s", body)
	}
}

// --- GET/POST /dns/cluster ---

func withScratchDNSClusterConfigForAPI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "named.conf.options")
	origPath := DNSClusterConfigPath
	DNSClusterConfigPath = path
	t.Cleanup(func() { DNSClusterConfigPath = origPath })

	origRestart := dnsRestartServiceRun
	dnsRestartServiceRun = func() {}
	t.Cleanup(func() { dnsRestartServiceRun = origRestart })
	return path
}

func TestAPIDNSClusterPostRequiresJSON(t *testing.T) {
	withScratchDNSClusterConfigForAPI(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/x-www-form-urlencoded", strings.NewReader("action=enable"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIDNSClusterGetReturnsExtractedConfig(t *testing.T) {
	path := withScratchDNSClusterConfigForAPI(t)
	os.WriteFile(path, []byte(testNamedConfOptionsEnabled), 0644)

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Get(srv.URL + "/dns/cluster")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["enabled"] != true {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterGetOnMissingFileReturns200WithErrorBody(t *testing.T) {
	withScratchDNSClusterConfigForAPI(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Get(srv.URL + "/dns/cluster")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (error stays in body, not status), got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"error"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterEnableDisable(t *testing.T) {
	path := withScratchDNSClusterConfigForAPI(t)
	os.WriteFile(path, []byte(testNamedConfOptionsDisabled), 0644)

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/json", strings.NewReader(`{"action":"enable"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "DNS cluster enabled successfully.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterCreateRequiresIP(t *testing.T) {
	withScratchDNSClusterConfigForAPI(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/json", strings.NewReader(`{"action":"create"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "ip is required") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterCreateRejectsInvalidIPFormat(t *testing.T) {
	withScratchDNSClusterConfigForAPI(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/json", strings.NewReader(`{"action":"create","ip":"not-an-ip"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid IP address format.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterCreateRejectsIPv6(t *testing.T) {
	withScratchDNSClusterConfigForAPI(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/json", strings.NewReader(`{"action":"create","ip":"::1"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Only IPv4 addresses are currently supported.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterCreateRejectsExistingIP(t *testing.T) {
	path := withScratchDNSClusterConfigForAPI(t)
	os.WriteFile(path, []byte(testNamedConfOptionsEnabled), 0644)

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/json", strings.NewReader(`{"action":"create","ip":"1.2.3.4"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "IP address already exists in configuration.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterCreateUnreachableSlaveReturns400(t *testing.T) {
	path := withScratchDNSClusterConfigForAPI(t)
	os.WriteFile(path, []byte(testNamedConfOptionsDisabled), 0644)

	origRNDC := dnsRNDCCommandRun
	dnsRNDCCommandRun = func(slaveIP string, args ...string) (bool, string) { return false, "" }
	t.Cleanup(func() { dnsRNDCCommandRun = origRNDC })

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/json", strings.NewReader(`{"action":"create","ip":"9.9.9.9"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Cannot reach 9.9.9.9 via rndc") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterCreateSuccessAddsIP(t *testing.T) {
	path := withScratchDNSClusterConfigForAPI(t)
	// addIPToConfig only rewrites uncommented allow-transfer/also-notify
	// blocks, so this needs the enabled fixture (the disabled one's blocks
	// are commented out and won't match).
	os.WriteFile(path, []byte(testNamedConfOptionsEnabled), 0644)

	origRNDC := dnsRNDCCommandRun
	dnsRNDCCommandRun = func(slaveIP string, args ...string) (bool, string) {
		return true, "number of zones: 3"
	}
	t.Cleanup(func() { dnsRNDCCommandRun = origRNDC })

	a := &APIDNS{PublicIP: "203.0.113.10"}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/json", strings.NewReader(`{"action":"create","ip":"9.9.9.9"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "IP 9.9.9.9 added to DNS cluster successfully.") {
		t.Fatalf("unexpected body: %s", body)
	}

	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "9.9.9.9") {
		t.Fatalf("expected config file updated, got %s", content)
	}
}

func TestAPIDNSClusterDeleteRequiresIP(t *testing.T) {
	withScratchDNSClusterConfigForAPI(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/json", strings.NewReader(`{"action":"delete"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIDNSClusterDeleteMissingIPReturns404(t *testing.T) {
	path := withScratchDNSClusterConfigForAPI(t)
	os.WriteFile(path, []byte(testNamedConfOptionsDisabled), 0644)

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/json", strings.NewReader(`{"action":"delete","ip":"9.9.9.9"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "not found in DNS cluster configuration.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterDeleteSuccess(t *testing.T) {
	path := withScratchDNSClusterConfigForAPI(t)
	os.WriteFile(path, []byte(testNamedConfOptionsEnabled), 0644)

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/json", strings.NewReader(`{"action":"delete","ip":"1.2.3.4"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "IP 1.2.3.4 removed from DNS cluster successfully.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterInvalidActionReturns400(t *testing.T) {
	withScratchDNSClusterConfigForAPI(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/cluster", "application/json", strings.NewReader(`{"action":"bogus"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid action. Use enable, disable, create, or delete.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// --- GET /dns/cluster/{ip} ---

func TestAPIDNSClusterNodeInfoRNDCSuccess(t *testing.T) {
	origRNDC := dnsRNDCCommandRun
	dnsRNDCCommandRun = func(slaveIP string, args ...string) (bool, string) {
		return true, "number of zones: 5\n"
	}
	t.Cleanup(func() { dnsRNDCCommandRun = origRNDC })

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Get(srv.URL + "/dns/cluster/1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["status"] != "success" || decoded["method"] != "rndc" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterNodeInfoTimeoutMessageHasNoDocsURL(t *testing.T) {
	origRNDC := dnsRNDCCommandRun
	dnsRNDCCommandRun = func(slaveIP string, args ...string) (bool, string) { return false, "" }
	t.Cleanup(func() { dnsRNDCCommandRun = origRNDC })

	origSSH := dnsSSHRun
	dnsSSHRun = func(host string, timeout time.Duration, remoteCmd string) (int, string, string, bool, error) {
		return 0, "", "", true, errors.New("context deadline exceeded")
	}
	t.Cleanup(func() { dnsSSHRun = origSSH })

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Get(srv.URL + "/dns/cluster/1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	// This route's timeout message is deliberately shorter than the HTML
	// dns-cluster page's (no docs URL suffix).
	if decoded["status"] != "timeout" || decoded["error"] != "Connection timed out." {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSClusterNodeInfoSSHFallback(t *testing.T) {
	origRNDC := dnsRNDCCommandRun
	dnsRNDCCommandRun = func(slaveIP string, args ...string) (bool, string) { return false, "" }
	t.Cleanup(func() { dnsRNDCCommandRun = origRNDC })

	origSSH := dnsSSHRun
	dnsSSHRun = func(host string, timeout time.Duration, remoteCmd string) (int, string, string, bool, error) {
		return 0, "Linux myhost 5.4.0\n", "", false, nil
	}
	t.Cleanup(func() { dnsSSHRun = origSSH })

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Get(srv.URL + "/dns/cluster/1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["status"] != "success" || decoded["method"] != "ssh" {
		t.Fatalf("unexpected body: %s", body)
	}
}

// --- GET/POST /dns/zone-templates ---

func TestAPIDNSZoneTemplatesGet(t *testing.T) {
	withScratchDNSTemplatePaths(t)
	os.WriteFile(DNSZoneTemplateIPv4Path, []byte("ipv4 tmpl"), 0644)
	os.WriteFile(DNSZoneTemplateIPv6Path, []byte("ipv6 tmpl"), 0644)

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Get(srv.URL + "/dns/zone-templates")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]string
	json.Unmarshal(body, &decoded)
	if decoded["zone_template_ipv4"] != "ipv4 tmpl" || decoded["zone_template_ipv6"] != "ipv6 tmpl" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDNSZoneTemplatesPostRequiresJSON(t *testing.T) {
	withScratchDNSTemplatePaths(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/zone-templates", "application/x-www-form-urlencoded", strings.NewReader("zone_template_ipv4=x"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIDNSZoneTemplatesPostUpdatesOnlySubmittedFields(t *testing.T) {
	withScratchDNSTemplatePaths(t)
	os.WriteFile(DNSZoneTemplateIPv4Path, []byte("old ipv4"), 0644)
	os.WriteFile(DNSZoneTemplateIPv6Path, []byte("old ipv6"), 0644)

	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/zone-templates", "application/json", strings.NewReader(`{"zone_template_ipv4":"new ipv4"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Template updated successfully!") {
		t.Fatalf("unexpected body: %s", body)
	}

	ipv4, _ := os.ReadFile(DNSZoneTemplateIPv4Path)
	if string(ipv4) != "new ipv4" {
		t.Fatalf("expected ipv4 updated, got %q", ipv4)
	}
	ipv6, _ := os.ReadFile(DNSZoneTemplateIPv6Path)
	if string(ipv6) != "old ipv6" {
		t.Fatalf("expected ipv6 untouched, got %q", ipv6)
	}
}

func TestAPIDNSZoneTemplatesPostNonStringValueCrashesTo500(t *testing.T) {
	withScratchDNSTemplatePaths(t)
	a := &APIDNS{}
	srv, client := newAPIDNSTestServer(t, a)

	resp, err := client.Post(srv.URL+"/dns/zone-templates", "application/json", strings.NewReader(`{"zone_template_ipv4":123}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 (write_file() would TypeError on a non-str value), got %d", resp.StatusCode)
	}
}
