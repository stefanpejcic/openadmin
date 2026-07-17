package handlers

import (
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

func withScratchUpdatesPaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origConfig, origEnv, origLogDir := UpdatesConfigFilePath, UpdatesEnvPath, UpdatesLogDir
	UpdatesConfigFilePath = filepath.Join(dir, "openpanel.config")
	UpdatesEnvPath = filepath.Join(dir, ".env")
	UpdatesLogDir = filepath.Join(dir, "updates") + "/"
	t.Cleanup(func() {
		UpdatesConfigFilePath, UpdatesEnvPath, UpdatesLogDir = origConfig, origEnv, origLogDir
	})
}

func newUpdatesTestServer(t *testing.T, u *Updates) (*httptest.Server, *http.Client) {
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
	db.CreateUser("caller", hash, "admin")
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	u.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/docker-tags", u.ServeDockerTags)
	mux.HandleFunc("POST /api/docker-tags", u.ServeDockerTags)
	mux.HandleFunc("POST /settings/updates/update_now", u.ServeUpdateNow)
	mux.HandleFunc("GET /settings/updates", u.ServeUpdates)
	mux.HandleFunc("POST /settings/updates", u.ServeUpdates)
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

func TestFetchDockerTagsSortsDescendingStringOrder(t *testing.T) {
	orig := updatesFetchTagsRun
	updatesFetchTagsRun = func() ([]string, error) { return []string{"1.0", "latest", "2.0", "1.5"}, nil }
	t.Cleanup(func() { updatesFetchTagsRun = orig })

	tags, err := fetchDockerTags()
	if err != nil {
		t.Fatal(err)
	}
	// Plain string sort descending: "2.0" > "1.5" > "1.0" lexicographically too here.
	want := []string{"2.0", "1.5", "1.0"}
	for i, w := range want {
		if tags[i] != w {
			t.Fatalf("expected %v, got %v", want, tags)
		}
	}
}

func TestIsVersionLikeTag(t *testing.T) {
	cases := map[string]bool{"1.2.3": true, "latest": false, "": false, "1.2-beta": false, "123": true}
	for in, want := range cases {
		if got := isVersionLikeTag(in); got != want {
			t.Errorf("isVersionLikeTag(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCompareVersionTags(t *testing.T) {
	if compareVersionTags("1.9", "1.10") >= 0 {
		t.Fatal("expected numeric compare: 1.9 < 1.10")
	}
	if compareVersionTags("2.0", "1.99") <= 0 {
		t.Fatal("expected 2.0 > 1.99 numerically")
	}
}

func TestGetLatestVersionUsesDockerHubNumericSort(t *testing.T) {
	origHub := updatesDockerHubTagsRun
	updatesDockerHubTagsRun = func() ([]string, error) { return []string{"1.9", "1.10", "latest"}, nil }
	t.Cleanup(func() { updatesDockerHubTagsRun = origHub })

	if got := getLatestVersion(); got != "1.10" {
		t.Fatalf("expected numeric max 1.10, got %q", got)
	}
}

func TestGetLatestVersionFallsBackOnEmptyVersions(t *testing.T) {
	origHub := updatesDockerHubTagsRun
	updatesDockerHubTagsRun = func() ([]string, error) { return []string{"latest", "beta"}, nil }
	t.Cleanup(func() { updatesDockerHubTagsRun = origHub })

	origFallback := updatesFallbackVersionRun
	updatesFallbackVersionRun = func() (string, error) { return "3.2.1", nil }
	t.Cleanup(func() { updatesFallbackVersionRun = origFallback })

	if got := getLatestVersion(); got != "3.2.1" {
		t.Fatalf("expected fallback version when no numeric tags found, got %q", got)
	}
}

func TestGetLatestVersionReturnsZeroWhenBothFail(t *testing.T) {
	origHub := updatesDockerHubTagsRun
	updatesDockerHubTagsRun = func() ([]string, error) { return nil, &ftpStubError{"network down"} }
	t.Cleanup(func() { updatesDockerHubTagsRun = origHub })

	origFallback := updatesFallbackVersionRun
	updatesFallbackVersionRun = func() (string, error) { return "", &ftpStubError{"also down"} }
	t.Cleanup(func() { updatesFallbackVersionRun = origFallback })

	if got := getLatestVersion(); got != "0.0.0" {
		t.Fatalf("expected 0.0.0 fallback, got %q", got)
	}
}

func TestWriteEnvVersionCreatesMissingFile(t *testing.T) {
	withScratchUpdatesPaths(t)

	if err := writeEnvVersion("1.2.3"); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(UpdatesEnvPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != `VERSION="1.2.3"`+"\n" {
		t.Fatalf("unexpected content: %q", saved)
	}
}

func TestWriteEnvVersionReplacesExisting(t *testing.T) {
	withScratchUpdatesPaths(t)
	os.WriteFile(UpdatesEnvPath, []byte("# header\nVERSION=\"1.0.0\"\nOTHER=\"x\"\n"), 0644)

	if err := writeEnvVersion("2.0.0"); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(UpdatesEnvPath)
	got := string(saved)
	if !strings.Contains(got, `VERSION="2.0.0"`) {
		t.Fatalf("expected VERSION replaced, got %q", got)
	}
	if !strings.Contains(got, "# header\n") || !strings.Contains(got, `OTHER="x"`) {
		t.Fatalf("expected other lines preserved, got %q", got)
	}
}

func TestWriteEnvVersionAppendsWhenNoExistingLine(t *testing.T) {
	withScratchUpdatesPaths(t)
	os.WriteFile(UpdatesEnvPath, []byte("OTHER=\"x\"\n"), 0644)

	if err := writeEnvVersion("2.0.0"); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(UpdatesEnvPath)
	if !strings.Contains(string(saved), `VERSION="2.0.0"`) {
		t.Fatalf("expected VERSION appended, got %q", saved)
	}
}

func TestGetOpUpdateLogsSortsByTimestampDesc(t *testing.T) {
	withScratchUpdatesPaths(t)
	os.MkdirAll(UpdatesLogDir, 0755)
	os.WriteFile(filepath.Join(UpdatesLogDir, "old.log"), []byte("x"), 0644)
	time.Sleep(1100 * time.Millisecond)
	os.WriteFile(filepath.Join(UpdatesLogDir, "new.log"), []byte("x"), 0644)

	logs := getOpUpdateLogs()
	if len(logs) != 2 || logs[0].File != "new.log" || logs[1].File != "old.log" {
		t.Fatalf("expected newest-first order, got %+v", logs)
	}
}

func TestGetOpUpdateLogsMissingDirReturnsNil(t *testing.T) {
	withScratchUpdatesPaths(t) // dir never created
	if got := getOpUpdateLogs(); got != nil {
		t.Fatalf("expected nil for missing dir, got %+v", got)
	}
}

func TestServeDockerTagsGet(t *testing.T) {
	origRun := updatesFetchTagsRun
	updatesFetchTagsRun = func() ([]string, error) { return []string{"1.0", "latest"}, nil }
	t.Cleanup(func() { updatesFetchTagsRun = origRun })

	u := &Updates{}
	srv, client := newUpdatesTestServer(t, u)

	resp, err := client.Get(srv.URL + "/api/docker-tags")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "1.0") || strings.Contains(string(body), "latest") {
		t.Fatalf("expected latest excluded, got %s", body)
	}
}

func TestServeDockerTagsPostMissingVersion(t *testing.T) {
	withScratchUpdatesPaths(t)

	u := &Updates{}
	srv, client := newUpdatesTestServer(t, u)

	resp, err := client.PostForm(srv.URL+"/api/docker-tags", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Version not provided") {
		t.Fatalf("expected error flash, got %s", truncate(string(body)))
	}
}

func TestServeDockerTagsPostInvalidFormat(t *testing.T) {
	withScratchUpdatesPaths(t)

	u := &Updates{}
	srv, client := newUpdatesTestServer(t, u)

	resp, err := client.PostForm(srv.URL+"/api/docker-tags", url.Values{"version": {"not-a-version"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid version format") {
		t.Fatalf("expected invalid-format flash, got %s", truncate(string(body)))
	}
}

func TestServeDockerTagsPostSuccessPullsAndComposes(t *testing.T) {
	withScratchUpdatesPaths(t)

	var pulledVersion string
	composeCalled := false
	origPull, origCompose := updatesPullImageRun, updatesComposeUpRun
	updatesPullImageRun = func(v string) error { pulledVersion = v; return nil }
	updatesComposeUpRun = func() error { composeCalled = true; return nil }
	t.Cleanup(func() { updatesPullImageRun, updatesComposeUpRun = origPull, origCompose })

	u := &Updates{}
	srv, client := newUpdatesTestServer(t, u)

	resp, err := client.PostForm(srv.URL+"/api/docker-tags", url.Values{"version": {"1.2.3"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if pulledVersion != "1.2.3" || !composeCalled {
		t.Fatalf("expected pull+compose called, pulledVersion=%q composeCalled=%v", pulledVersion, composeCalled)
	}
	if !strings.Contains(string(body), "Downgraded to version 1.2.3 successfully.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	saved, _ := os.ReadFile(UpdatesEnvPath)
	if !strings.Contains(string(saved), `VERSION="1.2.3"`) {
		t.Fatalf("expected .env updated, got %q", saved)
	}
}

func TestServeDockerTagsPostPullFailure(t *testing.T) {
	withScratchUpdatesPaths(t)

	origPull := updatesPullImageRun
	updatesPullImageRun = func(v string) error { return &ftpStubError{"no such image"} }
	t.Cleanup(func() { updatesPullImageRun = origPull })

	u := &Updates{}
	srv, client := newUpdatesTestServer(t, u)

	resp, err := client.PostForm(srv.URL+"/api/docker-tags", url.Values{"version": {"1.2.3"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Command failed: no such image") {
		t.Fatalf("expected pull-failure flash, got %s", truncate(string(body)))
	}
}

func TestServeUpdateNowSuccessAndFailure(t *testing.T) {
	origRun := updatesUpdateNowRun
	updatesUpdateNowRun = func() error { return nil }
	u := &Updates{}
	srv, client := newUpdatesTestServer(t, u)

	resp, err := client.PostForm(srv.URL+"/settings/updates/update_now", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Update process started successfully.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	updatesUpdateNowRun = func() error { return &ftpStubError{"exec failed"} }
	resp2, err := client.PostForm(srv.URL+"/settings/updates/update_now", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(body2), "Failed to start the update process") {
		t.Fatalf("expected failure flash, got %s", truncate(string(body2)))
	}
	t.Cleanup(func() { updatesUpdateNowRun = origRun })
}

func TestServeUpdatesPostInvalidPreferenceFlashesInsteadOfCrashing(t *testing.T) {
	withScratchUpdatesPaths(t)
	os.WriteFile(UpdatesConfigFilePath, []byte("[PANEL]\nautoupdate=on\nautopatch=on\n"), 0644)

	origHub := updatesDockerHubTagsRun
	updatesDockerHubTagsRun = func() ([]string, error) { return nil, &ftpStubError{"skip"} }
	t.Cleanup(func() { updatesDockerHubTagsRun = origHub })
	origFallback := updatesFallbackVersionRun
	updatesFallbackVersionRun = func() (string, error) { return "0.0.0", nil }
	t.Cleanup(func() { updatesFallbackVersionRun = origFallback })

	u := &Updates{}
	srv, client := newUpdatesTestServer(t, u)

	resp, err := client.PostForm(srv.URL+"/settings/updates", url.Values{"preference": {"bogus"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (fixed from the original's 500 crash) for an unrecognized preference, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid update preference selected.") {
		t.Fatalf("expected a graceful validation flash, got %s", truncate(string(body)))
	}

	saved, _ := os.ReadFile(UpdatesConfigFilePath)
	if !strings.Contains(string(saved), "autoupdate=on") {
		t.Fatalf("expected the config to be left untouched, got %q", saved)
	}
}

func TestServeUpdatesPostValidPreferenceUpdatesConfig(t *testing.T) {
	withScratchUpdatesPaths(t)
	os.WriteFile(UpdatesConfigFilePath, []byte("[PANEL]\nautoupdate=on\nautopatch=on\nother=1\n"), 0644)

	orig := updatesDockerHubTagsRun
	updatesDockerHubTagsRun = func() ([]string, error) { return nil, &ftpStubError{"skip"} }
	t.Cleanup(func() { updatesDockerHubTagsRun = orig })
	origFallback := updatesFallbackVersionRun
	updatesFallbackVersionRun = func() (string, error) { return "0.0.0", nil }
	t.Cleanup(func() { updatesFallbackVersionRun = origFallback })

	u := &Updates{}
	srv, client := newUpdatesTestServer(t, u)

	resp, err := client.PostForm(srv.URL+"/settings/updates", url.Values{"preference": {"minor_only"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Update preferences saved successfully.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	saved, _ := os.ReadFile(UpdatesConfigFilePath)
	got := string(saved)
	if !strings.Contains(got, "autoupdate=off") || !strings.Contains(got, "autopatch=on") {
		t.Fatalf("expected minor_only preference applied, got %q", got)
	}
	if !strings.Contains(got, "other=1") {
		t.Fatalf("expected untouched line preserved, got %q", got)
	}
}

func TestServeUpdatesGetRendersHTML(t *testing.T) {
	withScratchUpdatesPaths(t)
	os.WriteFile(UpdatesConfigFilePath, []byte("[PANEL]\nautoupdate=on\nautopatch=off\n"), 0644)
	os.MkdirAll(UpdatesLogDir, 0755)
	os.WriteFile(filepath.Join(UpdatesLogDir, "2024-01-01.log"), []byte("x"), 0644)

	// mergeChrome's chrome-level "PanelVersion" (chromeSite, set once at
	// startup in main.go from the same underlying value) is merged into the
	// render map after this handler's own "PanelVersion" key, so it must
	// match here too or it silently overwrites the page's displayed
	// installed-version with an empty string -- exercising the same
	// production wiring as main.go, which feeds both from one source.
	origChromeSite := chromeSite
	InitChromeSiteInfo("", "", "", "10.0", "", false, "")
	t.Cleanup(func() { chromeSite = origChromeSite })

	origHub := updatesDockerHubTagsRun
	updatesDockerHubTagsRun = func() ([]string, error) { return []string{"10.5"}, nil }
	t.Cleanup(func() { updatesDockerHubTagsRun = origHub })

	u := &Updates{PanelVersion: "10.0"}
	srv, client := newUpdatesTestServer(t, u)

	resp, err := client.Get(srv.URL + "/settings/updates")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{
		"Update Settings",
		"10.0",
		"10.5",
		"Start update to 10.5",
		"2024-01-01.log",
		"Downgrade",
		"</html>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeUpdatesGetShowUpdateNowUsesNumericComparison(t *testing.T) {
	withScratchUpdatesPaths(t)
	os.WriteFile(UpdatesConfigFilePath, []byte("[PANEL]\nautoupdate=on\nautopatch=on\n"), 0644)

	origHub := updatesDockerHubTagsRun
	updatesDockerHubTagsRun = func() ([]string, error) { return []string{"9.0"}, nil }
	t.Cleanup(func() { updatesDockerHubTagsRun = origHub })

	// PanelVersion "10.0" is numerically greater than latest "9.0" (even
	// though lexicographically "9.0" > "10.0" as strings) -- fixed from
	// the original's naive string comparison, the button must NOT show
	// since no real update is available.
	u := &Updates{PanelVersion: "10.0"}
	srv, client := newUpdatesTestServer(t, u)

	resp, err := client.Get(srv.URL + "/settings/updates")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "Start update to 9.0") {
		t.Fatalf("expected the numeric comparison to correctly hide the update-now button, got %s", truncate(string(body)))
	}
}

func TestServeUpdatesGetShowUpdateNowWhenRealUpdateAvailable(t *testing.T) {
	withScratchUpdatesPaths(t)
	os.WriteFile(UpdatesConfigFilePath, []byte("[PANEL]\nautoupdate=on\nautopatch=on\n"), 0644)

	origHub := updatesDockerHubTagsRun
	updatesDockerHubTagsRun = func() ([]string, error) { return []string{"10.1"}, nil }
	t.Cleanup(func() { updatesDockerHubTagsRun = origHub })

	u := &Updates{PanelVersion: "10.0"}
	srv, client := newUpdatesTestServer(t, u)

	resp, err := client.Get(srv.URL + "/settings/updates")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Start update to 10.1") {
		t.Fatalf("expected the update-now button to show for a real newer version, got %s", truncate(string(body)))
	}
}
