package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func withScratchLogPaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origLogPaths, origUpdates, origCrash := LogPathsFile, UpdateLogsDir, CrashLogsDir
	LogPathsFile = filepath.Join(dir, "log_paths.json")
	UpdateLogsDir = filepath.Join(dir, "updates")
	CrashLogsDir = filepath.Join(dir, "crashlog")
	t.Cleanup(func() {
		LogPathsFile = origLogPaths
		UpdateLogsDir = origUpdates
		CrashLogsDir = origCrash
	})
}

func newLogsTestServer(t *testing.T, l *Logs) (*httptest.Server, *http.Client) {
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
	l.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /services/logs", l.ServeIndex)
	mux.HandleFunc("GET /services/logs/edit", l.ServeEditLogs)
	mux.HandleFunc("POST /services/logs/edit", l.ServeEditLogs)
	mux.HandleFunc("GET /services/logs/raw", l.ServeViewLog)
	mux.HandleFunc("POST /services/logs/raw", l.ServeViewLog)
	mux.HandleFunc("DELETE /services/logs/raw", l.ServeViewLog)
	mux.HandleFunc("GET /settings/updates/log/", l.ServeUpdateLogsSettings)
	mux.HandleFunc("GET /services/updates/log/raw", l.ServeViewUpdateLog)
	mux.HandleFunc("DELETE /services/updates/log/raw", l.ServeViewUpdateLog)
	mux.HandleFunc("GET /services/crashlogs/log/", l.ServeCrashlogsSettings)
	mux.HandleFunc("GET /services/crashlogs/log/raw", l.ServeViewCrashlogsLog)
	mux.HandleFunc("DELETE /services/crashlogs/log/raw", l.ServeViewCrashlogsLog)
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		for _, f := range auth.PopFlashes(w, r, sessions) {
			w.Write([]byte(f.Category + ": " + f.Message + "\n"))
		}
	})
	mux.HandleFunc("/services/edit", func(w http.ResponseWriter, r *http.Request) {
		for _, f := range auth.PopFlashes(w, r, sessions) {
			w.Write([]byte(f.Category + ": " + f.Message + "\n"))
		}
	})
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

func doMethod(t *testing.T, client *http.Client, method, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestLoadLogPathsFallback(t *testing.T) {
	withScratchLogPaths(t) // file not written -> missing
	got := loadLogPaths()
	if got["Caddy Logs"] != "caddy" {
		t.Fatalf("expected fallback map, got %+v", got)
	}
}

func TestLoadLogPathsFromFile(t *testing.T) {
	withScratchLogPaths(t)
	os.WriteFile(LogPathsFile, []byte(`{"Custom Log": "/tmp/custom.log"}`), 0644)
	got := loadLogPaths()
	if len(got) != 1 || got["Custom Log"] != "/tmp/custom.log" {
		t.Fatalf("expected only the configured entry, got %+v", got)
	}
}

func TestLoadLogPathsInvalidJSONFallsBack(t *testing.T) {
	withScratchLogPaths(t)
	os.WriteFile(LogPathsFile, []byte(`not json`), 0644)
	got := loadLogPaths()
	if got["Caddy Logs"] != "caddy" {
		t.Fatalf("expected fallback map on invalid JSON, got %+v", got)
	}
}

func TestIsDigitsOnly(t *testing.T) {
	cases := map[string]bool{"": false, "123": true, "-5": false, "12a": false, "0": true}
	for in, want := range cases {
		if got := isDigitsOnly(in); got != want {
			t.Errorf("isDigitsOnly(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTailLogLines(t *testing.T) {
	raw := "one\ntwo\nthree\nfour\n"
	if got := tailLogLines(raw, 2); got != "three\nfour\n" {
		t.Fatalf("expected last 2 lines, got %q", got)
	}
	if got := tailLogLines(raw, 100); got != raw {
		t.Fatalf("expected whole content when n exceeds line count, got %q", got)
	}
	if got := tailLogLines(raw, 0); got != "" {
		t.Fatalf("expected empty string for n=0, got %q", got)
	}
	// Last line without a trailing newline should still round-trip.
	noTrailing := "one\ntwo"
	if got := tailLogLines(noTrailing, 1); got != "two" {
		t.Fatalf("expected %q, got %q", "two", got)
	}
}

func TestSafeJoinOr400(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.log"), []byte("hi"), 0644)

	if _, ok := safeJoinOr400(dir, ""); ok {
		t.Fatal("expected empty filename to be rejected")
	}
	if got, ok := safeJoinOr400(dir, "a.log"); !ok || got != filepath.Join(dir, "a.log") {
		t.Fatalf("expected a.log to resolve inside dir, got %q ok=%v", got, ok)
	}
	if _, ok := safeJoinOr400(dir, "../../../etc/passwd"); ok {
		t.Fatal("expected path traversal to be rejected")
	}
	// Non-existent file within the dir is still accepted -- existence is
	// checked separately by callers.
	if got, ok := safeJoinOr400(dir, "missing.log"); !ok || got != filepath.Join(dir, "missing.log") {
		t.Fatalf("expected missing.log to still resolve inside dir, got %q ok=%v", got, ok)
	}
}

func TestServeIndexGroupsLogs(t *testing.T) {
	withScratchLogPaths(t)
	os.WriteFile(LogPathsFile, []byte(`{"OpenAdmin Access Log": "/var/log/x.log", "OpenPanel Access Log": "/var/log/y.log", "Syslog": "/var/log/syslog"}`), 0644)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/logs")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{"OpenAdmin Access Log", "OpenPanel Access Log", "Syslog", "Log Viewer", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeEditLogsGetDefault(t *testing.T) {
	withScratchLogPaths(t) // no config file yet

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/logs/edit")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"<textarea", "Edit Log Paths", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeEditLogsPostSavesValidJSON(t *testing.T) {
	withScratchLogPaths(t)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.PostForm(srv.URL+"/services/logs/edit", url.Values{"data": {`{"Custom": "/tmp/c.log"}`}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Config file updated successfully.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	saved, err := os.ReadFile(LogPathsFile)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]string
	if err := json.Unmarshal(saved, &parsed); err != nil || parsed["Custom"] != "/tmp/c.log" {
		t.Fatalf("expected saved config to round-trip, got %q (err=%v)", saved, err)
	}
}

func TestServeEditLogsPostInvalidJSONRedirectsToSelf(t *testing.T) {
	withScratchLogPaths(t)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.PostForm(srv.URL+"/services/logs/edit", url.Values{"data": {`not json`}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected a redirect, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/services/logs/edit" {
		t.Fatalf("expected redirect back to /services/logs/edit, got %q", loc)
	}
}

func TestServeEditLogsJSONQueryParam(t *testing.T) {
	withScratchLogPaths(t)
	os.WriteFile(LogPathsFile, []byte(`{"Custom": "/tmp/c.log"}`), 0644)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/logs/edit?json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"Custom":"/tmp/c.log"`) {
		t.Fatalf("expected raw JSON body, got %s", truncate(string(body)))
	}
}

func TestServeViewLogUnknownNameForbidden(t *testing.T) {
	withScratchLogPaths(t)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/logs/raw?log_name=Nope")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeViewLogGetFileTailsLastNLines(t *testing.T) {
	withScratchLogPaths(t)
	logFile := filepath.Join(t.TempDir(), "app.log")
	os.WriteFile(logFile, []byte("l1\nl2\nl3\nl4\n"), 0644)
	os.WriteFile(LogPathsFile, []byte(`{"App Log": "`+strings.ReplaceAll(logFile, `\`, `\\`)+`"}`), 0644)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/logs/raw?log_name=App+Log&lines=2")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `l3\nl4\n`) {
		t.Fatalf("expected last 2 lines in JSON content, got %s", truncate(string(body)))
	}
}

func TestServeViewLogGetFileAll(t *testing.T) {
	withScratchLogPaths(t)
	logFile := filepath.Join(t.TempDir(), "app.log")
	os.WriteFile(logFile, []byte("l1\nl2\n"), 0644)
	os.WriteFile(LogPathsFile, []byte(`{"App Log": "`+strings.ReplaceAll(logFile, `\`, `\\`)+`"}`), 0644)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/logs/raw?log_name=App+Log&lines=ALL")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `l1\nl2\n`) {
		t.Fatalf("expected full content, got %s", truncate(string(body)))
	}
}

func TestServeViewLogGetContainerUsesDockerRunner(t *testing.T) {
	withScratchLogPaths(t)
	os.WriteFile(LogPathsFile, []byte(`{"Caddy Logs": "caddy"}`), 0644)

	origRun := getDockerLogRun
	getDockerLogRun = func(container, lines string) (string, error) {
		if container != "caddy" || lines != "50" {
			t.Fatalf("unexpected args: %q %q", container, lines)
		}
		return "caddy log output", nil
	}
	t.Cleanup(func() { getDockerLogRun = origRun })

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/logs/raw?log_name=Caddy+Logs&lines=50")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "caddy log output") {
		t.Fatalf("expected docker log content, got %s", truncate(string(body)))
	}
}

func TestServeViewLogPostDownloadsFile(t *testing.T) {
	withScratchLogPaths(t)
	logFile := filepath.Join(t.TempDir(), "app.log")
	os.WriteFile(logFile, []byte("download me"), 0644)
	os.WriteFile(LogPathsFile, []byte(`{"App Log": "`+strings.ReplaceAll(logFile, `\`, `\\`)+`"}`), 0644)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Post(srv.URL+"/services/logs/raw?log_name=App+Log", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "download me" {
		t.Fatalf("expected file content, got %q", body)
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("expected attachment disposition, got %q", resp.Header.Get("Content-Disposition"))
	}
}

func TestServeViewLogDeleteProtectedContainerForbidden(t *testing.T) {
	withScratchLogPaths(t)
	os.WriteFile(LogPathsFile, []byte(`{"Caddy Logs": "caddy"}`), 0644)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp := doMethod(t, client, http.MethodDelete, srv.URL+"/services/logs/raw?log_name=Caddy+Logs")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeViewLogDeleteUnprotectedContainerRejectedNotCreated(t *testing.T) {
	withScratchLogPaths(t)
	os.WriteFile(LogPathsFile, []byte(`{"MailServer Log": "openadmin_mailserver"}`), 0644)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp := doMethod(t, client, http.MethodDelete, srv.URL+"/services/logs/raw?log_name=MailServer+Log")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a container-backed entry not in the protected list (fixed from silently creating a file), got %d: %s", resp.StatusCode, body)
	}
	if _, err := os.Stat("openadmin_mailserver"); !os.IsNotExist(err) {
		os.Remove("openadmin_mailserver")
		t.Fatal("expected no spurious file to be created for a container-backed log name")
	}
}

func TestServeViewLogDeleteTruncatesFile(t *testing.T) {
	withScratchLogPaths(t)
	logFile := filepath.Join(t.TempDir(), "app.log")
	os.WriteFile(logFile, []byte("some content"), 0644)
	os.WriteFile(LogPathsFile, []byte(`{"App Log": "`+strings.ReplaceAll(logFile, `\`, `\\`)+`"}`), 0644)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp := doMethod(t, client, http.MethodDelete, srv.URL+"/services/logs/raw?log_name=App+Log")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "emptied") {
		t.Fatalf("expected emptied message, got %s", truncate(string(body)))
	}

	remaining, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected file truncated to 0 bytes, got %q", remaining)
	}
}

func TestServeUpdateLogsSettingsListsGlob(t *testing.T) {
	withScratchLogPaths(t)
	os.MkdirAll(UpdateLogsDir, 0755)
	os.WriteFile(filepath.Join(UpdateLogsDir, "2026-01-01.log"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(UpdateLogsDir, "notes.txt"), []byte("x"), 0644)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/settings/updates/log/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"2026-01-01.log", "Update Logs Viewer", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
	if strings.Contains(string(body), "notes.txt") {
		t.Fatalf("expected .txt file NOT listed for update logs, got %s", truncate(string(body)))
	}
}

func TestServeViewUpdateLogGetAndDelete(t *testing.T) {
	withScratchLogPaths(t)
	os.MkdirAll(UpdateLogsDir, 0755)
	os.WriteFile(filepath.Join(UpdateLogsDir, "u.log"), []byte("update log content"), 0644)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/updates/log/raw?log_name=u.log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "update log content") {
		t.Fatalf("expected file content, got %s", truncate(string(body)))
	}

	delResp := doMethod(t, client, http.MethodDelete, srv.URL+"/services/updates/log/raw?log_name=u.log")
	delBody, _ := io.ReadAll(delResp.Body)
	delResp.Body.Close()
	if !strings.Contains(string(delBody), "emptied") {
		t.Fatalf("expected emptied message, got %s", truncate(string(delBody)))
	}
	if _, err := os.Stat(filepath.Join(UpdateLogsDir, "u.log")); !os.IsNotExist(err) {
		t.Fatal("expected the update log file to be actually removed by DELETE, not just truncated")
	}
}

func TestServeViewUpdateLogTraversalRejected(t *testing.T) {
	withScratchLogPaths(t)
	os.MkdirAll(UpdateLogsDir, 0755)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/updates/log/raw?log_name=" + url.QueryEscape("../../../etc/passwd"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal attempt, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeCrashlogsSettingsAndRawView(t *testing.T) {
	withScratchLogPaths(t)
	os.MkdirAll(CrashLogsDir, 0755)
	os.WriteFile(filepath.Join(CrashLogsDir, "crash1.txt"), []byte("boom"), 0644)

	l := &Logs{}
	srv, client := newLogsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/crashlogs/log/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"crash1.txt", "Crash Logs Viewer", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}

	rawResp, err := client.Get(srv.URL + "/services/crashlogs/log/raw?log_name=crash1.txt")
	if err != nil {
		t.Fatal(err)
	}
	rawBody, _ := io.ReadAll(rawResp.Body)
	rawResp.Body.Close()
	if !strings.Contains(string(rawBody), "boom") {
		t.Fatalf("expected crash log content, got %s", truncate(string(rawBody)))
	}
}
