package handlers

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var testPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")

const testSVG = `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10"/></svg>`

func TestBrandImageExt(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"png", testPNG, ".png"},
		{"jpeg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), ".jpg"},
		{"gif", []byte("GIF89a\x01\x00\x01\x00"), ".gif"},
		{"svg with xml declaration", []byte(testSVG), ".svg"},
		{"bare svg with bom", []byte("\xef\xbb\xbf<svg xmlns=\"http://www.w3.org/2000/svg\"/>"), ".svg"},
		{"html", []byte("<html><body><svg></svg></body></html>"), ""},
		{"text", []byte("just some text"), ""},
		{"zip", []byte("PK\x03\x04rest"), ""},
	}
	for _, c := range cases {
		got, err := brandImageExt(c.in)
		if got != c.want || (c.want == "") != (err != nil) {
			t.Errorf("%s: got %q, %v, want %q", c.name, got, err, c.want)
		}
	}
}

func postOpenpanelMultipart(t *testing.T, client *http.Client, target string, form url.Values, files map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vs := range form {
		for _, v := range vs {
			mw.WriteField(k, v)
		}
	}
	for field, b := range files {
		fw, err := mw.CreateFormFile(field, "upload.bin")
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(b)
	}
	mw.Close()
	resp, err := client.Post(target, mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status %d: %s", resp.StatusCode, truncate(string(body)))
	}
	return string(body)
}

func withScratchBrandDir(t *testing.T) string {
	t.Helper()
	orig := BrandDir
	BrandDir = filepath.Join(t.TempDir(), "brand")
	t.Cleanup(func() { BrandDir = orig })
	return BrandDir
}

func TestServeOpenpanelSettingsLogoUpload(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	dir := withScratchBrandDir(t)
	srv, client := newOpenpanelSettingsTestServer(t, &OpenpanelSettings{})
	target := srv.URL + "/settings/open-panel"

	form := validOpenpanelForm()
	form.Set("brand_name", "Acme")
	form.Set("logo", "https://example.com/old.png")
	form.Set("logo_dark", "https://example.com/dark.png")
	postOpenpanelMultipart(t, client, target, form, map[string][]byte{"logo_file": testPNG})

	saved, _ := os.ReadFile(OpenpanelSettingsConfigPath)
	if !regexp.MustCompile(`(?m)^logo=/brand/logo\.png\?v=[0-9a-f]{12}$`).Match(saved) {
		t.Fatalf("logo should point at the upload, config:\n%s", saved)
	}
	// the other fields of a multipart post still save
	for _, want := range []string{"brand_name=Acme", "logo_dark=https://example.com/dark.png"} {
		if !strings.Contains(string(saved), want) {
			t.Errorf("config is missing %q:\n%s", want, saved)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "logo.png")); err != nil || !bytes.Equal(b, testPNG) {
		t.Fatalf("logo.png not stored: %v", err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "logo.png")); info.Mode().Perm() != 0644 {
		t.Errorf("logo.png mode %v, want 0644", info.Mode().Perm())
	}

	// an svg upload replaces the png, the old file goes
	postOpenpanelMultipart(t, client, target, form, map[string][]byte{"logo_file": []byte(testSVG), "logo_dark_file": testPNG})
	saved, _ = os.ReadFile(OpenpanelSettingsConfigPath)
	if !strings.Contains(string(saved), "logo=/brand/logo.svg?v=") || !strings.Contains(string(saved), "logo_dark=/brand/logo_dark.png?v=") {
		t.Fatalf("both logos should point at uploads, config:\n%s", saved)
	}
	if _, err := os.Stat(filepath.Join(dir, "logo.png")); !os.IsNotExist(err) {
		t.Error("logo.png should be removed once logo.svg replaces it")
	}
}

func TestServeOpenpanelSettingsLogoUploadRejected(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	dir := withScratchBrandDir(t)
	srv, client := newOpenpanelSettingsTestServer(t, &OpenpanelSettings{})
	target := srv.URL + "/settings/open-panel"

	form := validOpenpanelForm()
	form.Set("logo", "https://example.com/keep.png")
	body := postOpenpanelMultipart(t, client, target, form, map[string][]byte{"logo_file": []byte("<html><script>alert(1)</script></html>")})
	if !strings.Contains(body, "Logo not uploaded, only PNG, JPEG, WebP, GIF or SVG images can be uploaded.") {
		t.Fatalf("expected a type error flash, got %s", truncate(body))
	}
	big := append(append([]byte{}, testPNG...), make([]byte, brandUploadMax)...)
	body = postOpenpanelMultipart(t, client, target, form, map[string][]byte{"logo_dark_file": big})
	if !strings.Contains(body, "Logo for dark backgrounds not uploaded, the file is larger than 1 MB.") {
		t.Fatalf("expected a size error flash, got %s", truncate(body))
	}
	saved, _ := os.ReadFile(OpenpanelSettingsConfigPath)
	if !strings.Contains(string(saved), "logo=https://example.com/keep.png") {
		t.Errorf("a rejected upload should keep the typed url, config:\n%s", saved)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("nothing should be written for rejected uploads, found %d files", len(entries))
	}
}

func TestServeBrandFile(t *testing.T) {
	dir := withScratchBrandDir(t)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "logo.svg"), []byte(testSVG), 0644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0644)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /brand/{file}", ServeBrandFile)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/brand/logo.svg?v=1", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("logo.svg: %d %v", rec.Code, rec.Header())
	}
	for _, p := range []string{"/brand/notes.txt", "/brand/logo.png", "/brand/..%2fx"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 404 {
			t.Errorf("%s: want 404, got %d", p, rec.Code)
		}
	}
}
