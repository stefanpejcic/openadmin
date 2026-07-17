package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/auth"
	"openadmin/internal/config"
)

func TestDemoModeServeReflectsConfig(t *testing.T) {
	dir := t.TempDir()
	origMain := config.OpenpanelConfigPath
	config.OpenpanelConfigPath = filepath.Join(dir, "openpanel.config")
	t.Cleanup(func() { config.OpenpanelConfigPath = origMain })
	os.WriteFile(config.OpenpanelConfigPath, []byte("[PANEL]\ndemo_mode=off\n"), 0644)

	su := &ServerUtils{Sessions: auth.NewManager("test-secret", false)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /server/demo-mode", su.ServeDemoMode)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/server/demo-mode")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{"Enable Demo Mode", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected the 'off' state's content in page, got %s", truncate(got))
		}
	}
}

func TestDemoModeEnablePostFlashesAndCallsRunner(t *testing.T) {
	called := false
	origRun := demoModeRun
	demoModeRun = func() error { called = true; return nil }
	t.Cleanup(func() { demoModeRun = origRun })

	su := &ServerUtils{Sessions: auth.NewManager("test-secret", false)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /server/demo-mode", su.ServeDemoMode)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.PostForm(srv.URL+"/server/demo-mode", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Demo mode is enabled.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
	if !called {
		t.Fatal("expected the demo mode runner to be invoked")
	}
}
