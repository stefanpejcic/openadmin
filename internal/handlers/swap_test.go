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
	"time"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/config"
)

func newSwapTestServer(t *testing.T, s *Swap, role string) (*httptest.Server, *http.Client) {
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
	db.CreateUser("caller", hash, role)
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	origNotif := NotificationsConfigPath
	NotificationsConfigPath = filepath.Join(dir, "notifications.ini")
	t.Cleanup(func() { NotificationsConfigPath = origNotif })

	sessions := auth.NewManager("test-secret", false)
	s.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /server/swap", s.ServeSwap)
	mux.HandleFunc("POST /server/swap/action/{action}", s.ServeSwapAction)
	mux.HandleFunc("GET /server/swap/action-status", s.ServeSwapActionStatus)
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

func TestParseSwapFree(t *testing.T) {
	orig := swapFreeRun
	swapFreeRun = func() (string, error) {
		return "               total        used        free      shared  buff/cache   available\n" +
			"Mem:            8675        5938        2236         906        1803        2737\n" +
			"Swap:           1023          51         972\n", nil
	}
	t.Cleanup(func() { swapFreeRun = orig })

	total, used := parseSwapFree()
	if total != 1023 || used != 51 {
		t.Fatalf("expected total=1023 used=51, got total=%d used=%d", total, used)
	}
}

func TestParseSwapDevices(t *testing.T) {
	orig := swapShowRun
	swapShowRun = func() (string, error) {
		return "/swapfile file 1073741824 0 -2\n", nil
	}
	t.Cleanup(func() { swapShowRun = orig })

	devices := parseSwapDevices()
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	d := devices[0]
	if d.Name != "/swapfile" || d.Type != "file" || d.SizeMB != 1024 || d.UsedMB != 0 || d.Priority != "-2" {
		t.Fatalf("unexpected device parse: %+v", d)
	}
}

func TestGetSwapStatusReadsThresholdFromNotificationsConfig(t *testing.T) {
	dir := t.TempDir()
	origNotif := NotificationsConfigPath
	NotificationsConfigPath = filepath.Join(dir, "notifications.ini")
	t.Cleanup(func() { NotificationsConfigPath = origNotif })

	data := config.Data{}
	data.Set("DEFAULT", "swap", "70")
	if err := config.Save(NotificationsConfigPath, data); err != nil {
		t.Fatal(err)
	}

	origFree, origShow := swapFreeRun, swapShowRun
	swapFreeRun = func() (string, error) { return "Swap:           1000        200         800\n", nil }
	swapShowRun = func() (string, error) { return "", nil }
	t.Cleanup(func() { swapFreeRun, swapShowRun = origFree, origShow })

	status := getSwapStatus()
	if status.ThresholdPercent != 70 {
		t.Fatalf("expected threshold 70, got %d", status.ThresholdPercent)
	}
	if status.TotalMB != 1000 || status.UsedMB != 200 || status.FreeMB != 800 || status.UsedPercent != 20 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestServeSwapGetRendersHTML(t *testing.T) {
	origFree, origShow := swapFreeRun, swapShowRun
	swapFreeRun = func() (string, error) { return "Swap:           1024           0        1024\n", nil }
	swapShowRun = func() (string, error) { return "/swapfile file 1073741824 0 -2\n", nil }
	t.Cleanup(func() { swapFreeRun, swapShowRun = origFree, origShow })

	s := &Swap{}
	srv, client := newSwapTestServer(t, s, "admin")

	resp, err := client.Get(srv.URL + "/server/swap")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(got))
	}
	for _, want := range []string{"Swap", "/swapfile", "1024 MB", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeSwapActionResizeRejectsTooSmall(t *testing.T) {
	s := &Swap{}
	srv, client := newSwapTestServer(t, s, "admin")

	resp, err := client.PostForm(srv.URL+"/server/swap/action/resize", url.Values{"size_mb": {"10"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a too-small swap size, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeSwapActionInvalidAction(t *testing.T) {
	s := &Swap{}
	srv, client := newSwapTestServer(t, s, "admin")

	resp, err := client.PostForm(srv.URL+"/server/swap/action/bogus", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid action, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeSwapActionDropSchedulesAndPolls(t *testing.T) {
	called := false
	origDrop := swapDropRun
	swapDropRun = func() error { called = true; return nil }
	t.Cleanup(func() { swapDropRun = origDrop })

	s := &Swap{}
	srv, client := newSwapTestServer(t, s, "admin")

	resp, err := client.PostForm(srv.URL+"/server/swap/action/drop", url.Values{})
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
		statusResp, err := client.Get(srv.URL + "/server/swap/action-status")
		if err != nil {
			t.Fatal(err)
		}
		statusBody, _ := io.ReadAll(statusResp.Body)
		statusResp.Body.Close()
		if strings.Contains(string(statusBody), `"done":true`) {
			if !strings.Contains(string(statusBody), `"success":true`) {
				t.Fatalf("expected success, got %s", statusBody)
			}
			if !called {
				t.Fatal("expected swapDropRun to have been called")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for drop action to complete")
}

func TestServeSwapActionResizeSchedulesAndPolls(t *testing.T) {
	var gotSizeMB int64
	origResize := swapResizeRun
	swapResizeRun = func(sizeMB int64) error { gotSizeMB = sizeMB; return nil }
	t.Cleanup(func() { swapResizeRun = origResize })

	s := &Swap{}
	srv, client := newSwapTestServer(t, s, "admin")

	resp, err := client.PostForm(srv.URL+"/server/swap/action/resize", url.Values{"size_mb": {"2048"}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		statusResp, err := client.Get(srv.URL + "/server/swap/action-status")
		if err != nil {
			t.Fatal(err)
		}
		statusBody, _ := io.ReadAll(statusResp.Body)
		statusResp.Body.Close()
		if strings.Contains(string(statusBody), `"done":true`) {
			if gotSizeMB != 2048 {
				t.Fatalf("expected swapResizeRun called with 2048, got %d", gotSizeMB)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for resize action to complete")
}

func TestServeSwapActionStatusNoActionYet(t *testing.T) {
	pendingSwapActionMu.Lock()
	pendingSwapAction = nil
	pendingSwapActionMu.Unlock()

	s := &Swap{}
	srv, client := newSwapTestServer(t, s, "admin")

	resp, err := client.Get(srv.URL + "/server/swap/action-status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "No action has run yet.") {
		t.Fatalf("expected placeholder message, got %s", body)
	}
}
