package handlers

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPodmanParseHighCriticalVulns(t *testing.T) {
	cases := []struct {
		name string
		json string
		want int
	}{
		{"no results", `{"Results":[]}`, 0},
		{"no vulnerabilities key", `{"Results":[{"Target":"x"}]}`, 0},
		{
			"mixed severities keeps only high/critical",
			`{"Results":[{"Vulnerabilities":[
				{"VulnerabilityID":"CVE-1","Severity":"HIGH"},
				{"VulnerabilityID":"CVE-2","Severity":"CRITICAL"},
				{"VulnerabilityID":"CVE-3","Severity":"MEDIUM"},
				{"VulnerabilityID":"CVE-4","Severity":"LOW"}
			]}]}`,
			2,
		},
		{
			"multiple results summed",
			`{"Results":[
				{"Vulnerabilities":[{"VulnerabilityID":"CVE-1","Severity":"HIGH"}]},
				{"Vulnerabilities":[{"VulnerabilityID":"CVE-2","Severity":"CRITICAL"},{"VulnerabilityID":"CVE-3","Severity":"CRITICAL"}]}
			]}`,
			3,
		},
	}
	for _, c := range cases {
		got, err := podmanParseHighCriticalVulns([]byte(c.json))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if len(got) != c.want {
			t.Fatalf("%s: got %d, want %d", c.name, len(got), c.want)
		}
	}
}

func TestPodmanParseHighCriticalVulnsFieldsMapped(t *testing.T) {
	raw := `{"Results":[{"Vulnerabilities":[{
		"VulnerabilityID":"CVE-2024-12345",
		"PkgName":"openssl",
		"InstalledVersion":"1.1.1",
		"FixedVersion":"1.1.2",
		"Severity":"CRITICAL",
		"Title":"Buffer overflow in OpenSSL",
		"PrimaryURL":"https://example.com/CVE-2024-12345"
	}]}]}`
	got, err := podmanParseHighCriticalVulns([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 detail, got %d", len(got))
	}
	want := podmanVulnDetail{
		ID: "CVE-2024-12345", Package: "openssl", InstalledVersion: "1.1.1",
		FixedVersion: "1.1.2", Severity: "CRITICAL", Title: "Buffer overflow in OpenSSL",
		URL: "https://example.com/CVE-2024-12345",
	}
	if got[0] != want {
		t.Fatalf("got %+v, want %+v", got[0], want)
	}
}

func TestPodmanParseHighCriticalVulnsInvalidJSON(t *testing.T) {
	if _, err := podmanParseHighCriticalVulns([]byte("not json")); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestPodmanEnsureTrivyRunSkipsInstallWhenAlreadyPresent(t *testing.T) {
	origInstalled, origInstall := trivyInstalled, trivyInstallRun
	t.Cleanup(func() { trivyInstalled, trivyInstallRun = origInstalled, origInstall })

	trivyInstalled = func() bool { return true }
	installCalled := false
	trivyInstallRun = func() error { installCalled = true; return nil }

	if err := podmanEnsureTrivyRun(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if installCalled {
		t.Fatal("expected trivyInstallRun not to be called when already installed")
	}
}

func TestPodmanEnsureTrivyRunInstallsWhenMissing(t *testing.T) {
	origInstalled, origInstall := trivyInstalled, trivyInstallRun
	t.Cleanup(func() { trivyInstalled, trivyInstallRun = origInstalled, origInstall })

	trivyInstalled = func() bool { return false }
	installCalled := false
	trivyInstallRun = func() error { installCalled = true; return nil }

	if err := podmanEnsureTrivyRun(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !installCalled {
		t.Fatal("expected trivyInstallRun to be called when trivy is missing")
	}
}

// newPodmanVulnTestServer wires just the bulk-action routes -- unlike most
// of this package's test servers, no session/login setup is needed since
// these handlers aren't wrapped in auth middleware at this layer (that's
// applied in main.go, not the handler itself).
func newPodmanVulnTestServer(t *testing.T, p *Podman) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /services/podman/images/bulk/{action}", p.ServePodmanImagesBulkAction)
	mux.HandleFunc("GET /services/podman/images/bulk-status", p.ServePodmanImagesBulkStatus)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, &http.Client{Jar: jar}
}

func TestServePodmanImagesBulkActionCheckVulnerabilitiesInstallsTrivyThenScans(t *testing.T) {
	origRunRun := podmanRunRun
	origInstalled, origInstall := trivyInstalled, trivyInstallRun
	origCheckVuln := podmanCheckImageVulnerabilitiesRun
	t.Cleanup(func() {
		podmanRunRun = origRunRun
		trivyInstalled, trivyInstallRun = origInstalled, origInstall
		podmanCheckImageVulnerabilitiesRun = origCheckVuln
	})

	podmanRunRun = func(args ...string) (string, error) {
		return `[{"Id":"abc123","Names":["docker.io/library/redis:7"],"Size":100}]`, nil
	}

	installCalled := false
	trivyInstalled = func() bool { return false }
	trivyInstallRun = func() error { installCalled = true; return nil }

	var scannedRefs []string
	podmanCheckImageVulnerabilitiesRun = func(ref string) ([]podmanVulnDetail, error) {
		scannedRefs = append(scannedRefs, ref)
		return []podmanVulnDetail{
			{ID: "CVE-2024-1", Package: "openssl", Severity: "CRITICAL"},
			{ID: "CVE-2024-2", Package: "libc", Severity: "HIGH"},
			{ID: "CVE-2024-3", Package: "curl", Severity: "HIGH"},
		}, nil
	}

	p := &Podman{}
	srv, client := newPodmanVulnTestServer(t, p)

	resp, err := client.PostForm(srv.URL+"/services/podman/images/bulk/check-vulnerabilities", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"scheduled":true`) {
		t.Fatalf("expected scheduled response, got %s", body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		statusResp, err := client.Get(srv.URL + "/services/podman/images/bulk-status")
		if err != nil {
			t.Fatal(err)
		}
		statusBody, _ := io.ReadAll(statusResp.Body)
		statusResp.Body.Close()
		if strings.Contains(string(statusBody), `"done":true`) {
			if !installCalled {
				t.Fatal("expected trivyInstallRun to have been called")
			}
			if len(scannedRefs) != 1 || scannedRefs[0] != "docker.io/library/redis:7" {
				t.Fatalf("expected exactly one scan of docker.io/library/redis:7, got %v", scannedRefs)
			}
			if !strings.Contains(string(statusBody), "1 image(s) have known HIGH/CRITICAL vulnerabilities") {
				t.Fatalf("expected vulnerable-image count in message, got %s", statusBody)
			}
			podmanVulnStatusMu.Lock()
			st, ok := podmanVulnStatusCache["docker.io/library/redis:7"]
			podmanVulnStatusMu.Unlock()
			if !ok || st.Count != 3 || len(st.Details) != 3 {
				t.Fatalf("expected cache entry with Count=3 and 3 Details, got %+v (present=%v)", st, ok)
			}
			if st.Details[0].ID != "CVE-2024-1" {
				t.Fatalf("expected Details to be preserved, got %+v", st.Details)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for bulk check-vulnerabilities to complete")
}

func TestServePodmanImagesBulkActionRejectsInvalidAction(t *testing.T) {
	p := &Podman{}
	srv, client := newPodmanVulnTestServer(t, p)

	resp, err := client.PostForm(srv.URL+"/services/podman/images/bulk/bogus", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "check-vulnerabilities") {
		t.Fatalf("expected error message to mention check-vulnerabilities, got %s", body)
	}
}

func TestServePodmanImageVulnerabilitiesRequiresRef(t *testing.T) {
	p := &Podman{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /services/podman/images/vulnerabilities", p.ServePodmanImageVulnerabilities)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/services/podman/images/vulnerabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 without a ref, got %d", resp.StatusCode)
	}
}

func TestServePodmanImageVulnerabilitiesReturnsCachedDetails(t *testing.T) {
	ref := "docker.io/library/nginx:latest"
	podmanVulnStatusMu.Lock()
	podmanVulnStatusCache[ref] = podmanVulnStatus{
		Count:   2,
		Details: []podmanVulnDetail{{ID: "CVE-2024-1", Package: "openssl", Severity: "CRITICAL"}, {ID: "CVE-2024-2", Package: "zlib", Severity: "HIGH"}},
	}
	podmanVulnStatusMu.Unlock()
	t.Cleanup(func() {
		podmanVulnStatusMu.Lock()
		delete(podmanVulnStatusCache, ref)
		podmanVulnStatusMu.Unlock()
	})

	p := &Podman{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /services/podman/images/vulnerabilities", p.ServePodmanImageVulnerabilities)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/services/podman/images/vulnerabilities?ref=" + url.QueryEscape(ref))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"checked":true`) || !strings.Contains(string(body), "CVE-2024-1") {
		t.Fatalf("expected cached details in response, got %s", body)
	}
}

// TestPodmanListImagesDedupesAdditionalImageStoreDuplicate guards against a
// regression where an image present via storage.conf's
// additionalimagestores (the shared-storage setup every hosting user's
// rootless podman shares with root) showed up twice in `podman images`
// output -- once as root's own writable copy, once again as a read-only
// view differing only by a "ReadOnly": true field -- and rendered as two
// identical rows on the Images tab.
func TestPodmanListImagesDedupesAdditionalImageStoreDuplicate(t *testing.T) {
	orig := podmanRunRun
	t.Cleanup(func() { podmanRunRun = orig })

	podmanRunRun = func(args ...string) (string, error) {
		return `[
			{"Id":"a14e639cf2a2","Names":["docker.io/tecnativa/docker-socket-proxy:latest"],"Size":100},
			{"Id":"a14e639cf2a2","Names":["docker.io/tecnativa/docker-socket-proxy:latest"],"Size":100,"ReadOnly":true}
		]`, nil
	}

	rows := podmanListImages(nil, nil)
	if len(rows) != 1 {
		t.Fatalf("expected the duplicate image ID to be deduped to 1 row, got %d: %+v", len(rows), rows)
	}
	if rows[0].Repository != "docker.io/tecnativa/docker-socket-proxy" {
		t.Fatalf("unexpected row: %+v", rows[0])
	}
}

// TestPodmanDeleteImageRunFallsBackToPlainRmiWhenNotInSharedStore guards
// against a regression found live: an image pulled before the
// shared-storage migration (or otherwise only present in root's own
// default context) makes `podman --root <sharedstore> rmi <id>` fail with
// "... : image not known" even though a plain `podman rmi <id>` against
// the default context deletes it just fine. Delete must fall back to the
// plain rmi in that specific case rather than reporting failure.
func TestPodmanDeleteImageRunFallsBackToPlainRmiWhenNotInSharedStore(t *testing.T) {
	orig := podmanRunRun
	origFixPerms := podmanFixSharedStorePermissionsRun
	t.Cleanup(func() { podmanRunRun = orig; podmanFixSharedStorePermissionsRun = origFixPerms })
	podmanFixSharedStorePermissionsRun = func(string) {}

	var calls [][]string
	podmanRunRun = func(args ...string) (string, error) {
		calls = append(calls, args)
		switch {
		case args[0] == "info":
			return `{"store":{"graphOptions":{"overlay.imagestore":"/var/lib/containers/shared-storage"}}}`, nil
		case args[0] == "--root":
			return "Error: deadbeef: image not known", fmt.Errorf("exit status 2")
		case args[0] == "rmi":
			return "Untagged: docker.io/openpanel/openpanel:2.0.0\nDeleted: deadbeef", nil
		}
		return "", fmt.Errorf("unexpected call: %v", args)
	}

	out, err := podmanDeleteImageRun("deadbeef")
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v (out=%s)", err, out)
	}
	if !strings.Contains(out, "Deleted: deadbeef") {
		t.Fatalf("expected fallback rmi's success output, got %s", out)
	}

	// Confirm both the --root attempt and the plain-rmi fallback actually
	// ran, in that order, rather than the fallback being skipped or the
	// first attempt never happening.
	sawRootAttempt, sawFallback := false, false
	for _, c := range calls {
		if len(c) > 0 && c[0] == "--root" {
			sawRootAttempt = true
		}
		if len(c) > 0 && c[0] == "rmi" {
			sawFallback = true
			if !sawRootAttempt {
				t.Fatal("expected the --root attempt to run before the plain-rmi fallback")
			}
		}
	}
	if !sawRootAttempt || !sawFallback {
		t.Fatalf("expected both a --root attempt and a plain-rmi fallback, got calls: %v", calls)
	}
}

func TestServePodmanImageVulnerabilitiesNotYetChecked(t *testing.T) {
	p := &Podman{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /services/podman/images/vulnerabilities", p.ServePodmanImageVulnerabilities)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/services/podman/images/vulnerabilities?ref=" + url.QueryEscape("docker.io/library/never-checked:latest"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"checked":false`) {
		t.Fatalf("expected checked:false for an unchecked ref, got %s", body)
	}
}
