package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestServeFileServesAllowlistedStaticFile(t *testing.T) {
	staticDir := t.TempDir()
	os.WriteFile(filepath.Join(staticDir, "robots.txt"), []byte("User-agent: *"), 0644)

	g := &GeneralStatic{StaticDir: staticDir}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{filename}", g.ServeFile)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/robots.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "User-agent: *" {
		t.Fatalf("expected robots.txt served, got %d %q", resp.StatusCode, body)
	}
}

func TestServeFileServesAllowlistedConfigFile(t *testing.T) {
	dir := t.TempDir()
	origConfigDir := GeneralConfigDir
	GeneralConfigDir = dir
	t.Cleanup(func() { GeneralConfigDir = origConfigDir })
	os.WriteFile(filepath.Join(dir, "shortcuts.json"), []byte(`{"a":1}`), 0644)

	g := &GeneralStatic{StaticDir: t.TempDir()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{filename}", g.ServeFile)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/shortcuts.json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != `{"a":1}` {
		t.Fatalf("expected shortcuts.json served, got %d %q", resp.StatusCode, body)
	}
}

func TestServeFileRejectsUnlistedFile(t *testing.T) {
	g := &GeneralStatic{StaticDir: t.TempDir()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{filename}", g.ServeFile)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/whatever.txt")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for a non-allowlisted filename, got %d", resp.StatusCode)
	}
}
