package handlers

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// staticFile is one embedded file with its etag and, for text types, a gzipped copy made once
type staticFile struct {
	raw  []byte
	gz   []byte
	etag string
}

// versioned asset names like jquery361.min.js or alpinejs3xx.min.js only change together with their name
var staticVersionedRE = regexp.MustCompile(`(?:\d{3}|\dxx)\.min\.(?:js|css)$`)

var staticCompressible = map[string]bool{".js": true, ".css": true, ".svg": true, ".json": true, ".html": true, ".txt": true, ".map": true, ".xml": true}

// staticCacheControl keeps versioned libraries for a week, everything else is revalidated with its etag so a new build shows up right away
func staticCacheControl(name string, hashed bool) string {
	switch {
	case hashed:
		// ?v= is the file's content hash from the asset template func, a new build gets a new url
		return "public, max-age=31536000, immutable"
	case staticVersionedRE.MatchString(name):
		return "public, max-age=604800, immutable"
	case strings.HasPrefix(name, "images/") || strings.HasSuffix(name, ".woff2") || strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".ico"):
		return "public, max-age=86400"
	}
	return "no-cache"
}

// StaticHandler serves the embedded static files gzipped and with etags, the plain FileServer sent neither so every page downloaded every script again
func StaticHandler(fsys fs.FS) http.Handler {
	fallback := http.FileServerFS(fsys)
	var mu sync.Mutex
	files := map[string]*staticFile{}

	load := func(name string) *staticFile {
		mu.Lock()
		defer mu.Unlock()
		if f, ok := files[name]; ok {
			return f
		}
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			files[name] = nil
			return nil
		}
		sum := sha256.Sum256(raw)
		f := &staticFile{raw: raw, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
		if staticCompressible[path.Ext(name)] && len(raw) > 1024 {
			var b bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
			_, _ = zw.Write(raw)
			_ = zw.Close()
			if b.Len() < len(raw) {
				f.gz = b.Bytes()
			}
		}
		files[name] = f
		return f
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		f := (*staticFile)(nil)
		if name != "" && !strings.HasSuffix(r.URL.Path, "/") {
			f = load(name)
		}
		if f == nil {
			// directories and missing files keep the old behavior
			fallback.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", staticCacheControl(name, r.URL.Query().Get("v") != ""))
		body, etag := f.raw, f.etag
		if f.gz != nil {
			h.Set("Vary", "Accept-Encoding")
			if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				body, etag = f.gz, strings.TrimSuffix(f.etag, `"`)+`-gz"`
				h.Set("Content-Encoding", "gzip")
			}
		}
		h.Set("ETag", etag)
		// ServeContent answers If-None-Match with a 304, handles HEAD and ranges, and picks the type from the extension
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	})
}
