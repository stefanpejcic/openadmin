package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestServeFileServesAllowlistedStaticFile(t *testing.T) {
	fsys := fstest.MapFS{
		"robots.txt": &fstest.MapFile{Data: []byte("User-agent: *")},
	}

	g := &GeneralStatic{Static: fsys}
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

	g := &GeneralStatic{Static: fstest.MapFS{}}
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

func TestServeFileOverridesStaticFileFromAdminDir(t *testing.T) {
	fsys := fstest.MapFS{
		"robots.txt": &fstest.MapFile{Data: []byte("default")},
	}

	overrideDir := t.TempDir()
	origOverrideDir := GeneralOverrideDir
	GeneralOverrideDir = overrideDir
	t.Cleanup(func() { GeneralOverrideDir = origOverrideDir })
	os.WriteFile(filepath.Join(overrideDir, "robots.txt"), []byte("custom"), 0644)

	g := &GeneralStatic{Static: fsys}
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
	if resp.StatusCode != http.StatusOK || string(body) != "custom" {
		t.Fatalf("expected admin override to win, got %d %q", resp.StatusCode, body)
	}
}

func TestServeFileFallsBackToDefaultWhenNoOverride(t *testing.T) {
	fsys := fstest.MapFS{
		"robots.txt": &fstest.MapFile{Data: []byte("default")},
	}

	origOverrideDir := GeneralOverrideDir
	GeneralOverrideDir = t.TempDir() // exists, but has no robots.txt in it
	t.Cleanup(func() { GeneralOverrideDir = origOverrideDir })

	g := &GeneralStatic{Static: fsys}
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
	if resp.StatusCode != http.StatusOK || string(body) != "default" {
		t.Fatalf("expected bundled default, got %d %q", resp.StatusCode, body)
	}
}

func TestServeFileServesCustomCSSWhenPresent(t *testing.T) {
	overrideDir := t.TempDir()
	origOverrideDir := GeneralOverrideDir
	GeneralOverrideDir = overrideDir
	t.Cleanup(func() { GeneralOverrideDir = origOverrideDir })
	os.WriteFile(filepath.Join(overrideDir, "custom.css"), []byte("body{color:red}"), 0644)

	g := &GeneralStatic{Static: fstest.MapFS{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{filename}", g.ServeFile)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/custom.css")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "body{color:red}" {
		t.Fatalf("expected custom.css served, got %d %q", resp.StatusCode, body)
	}
}

func TestServeFile404sForCustomCSSWhenAbsent(t *testing.T) {
	origOverrideDir := GeneralOverrideDir
	GeneralOverrideDir = t.TempDir() // exists, but no custom.css in it
	t.Cleanup(func() { GeneralOverrideDir = origOverrideDir })

	g := &GeneralStatic{Static: fstest.MapFS{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{filename}", g.ServeFile)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/custom.css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when no custom.css is present, got %d", resp.StatusCode)
	}
}

func TestServeFileRejectsUnlistedFile(t *testing.T) {
	g := &GeneralStatic{Static: fstest.MapFS{}}
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
