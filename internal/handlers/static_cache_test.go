package handlers

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticHandler(t *testing.T) {
	big := strings.Repeat("console.log('openadmin');\n", 200)
	fsys := fstest.MapFS{
		"dist/js/jquery361.min.js": {Data: []byte(big)},
		"dist/output.css":          {Data: []byte(strings.Repeat(".a{color:red}", 200))},
		"images/logo.png":          {Data: []byte("png")},
		"tiny.js":                  {Data: []byte("x()")},
	}
	h := StaticHandler(fsys)
	get := func(path string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	w := get("/dist/js/jquery361.min.js", map[string]string{"Accept-Encoding": "gzip, br"})
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("gzip js: %d %v", w.Code, w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := io.ReadAll(zr); string(body) != big {
		t.Fatal("gzipped body doesn't match the file")
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=604800") {
		t.Errorf("versioned file cache-control = %q", cc)
	}
	etag := w.Header().Get("ETag")
	if w2 := get("/dist/js/jquery361.min.js", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": etag}); w2.Code != http.StatusNotModified {
		t.Errorf("revalidation got %d, want 304", w2.Code)
	}

	w = get("/dist/output.css", nil)
	if w.Header().Get("Content-Encoding") != "" || w.Body.Len() != 200*13 {
		t.Errorf("plain css without gzip: enc=%q len=%d", w.Header().Get("Content-Encoding"), w.Body.Len())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" || w.Header().Get("ETag") == "" {
		t.Errorf("unversioned file should revalidate with an etag, got %q %q", cc, w.Header().Get("ETag"))
	}
	if cc := get("/images/logo.png", nil).Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("image cache-control = %q", cc)
	}
	if enc := get("/tiny.js", map[string]string{"Accept-Encoding": "gzip"}).Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("tiny files aren't worth gzipping, got %q", enc)
	}
	if code := get("/missing.js", nil).Code; code != http.StatusNotFound {
		t.Errorf("missing file got %d", code)
	}
}
