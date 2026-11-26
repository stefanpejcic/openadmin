package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// BrandDir is where uploaded logos go, the user panel serves it at /brand/ (the container mounts /etc/openpanel)
var BrandDir = "/etc/openpanel/openpanel/brand"

const brandUploadMax = 1 << 20

// brandUploadKeys are the config keys that take an upload, the file input is <key>_file
var brandUploadKeys = []string{"logo", "logo_dark"}

var brandExts = []string{".png", ".jpg", ".webp", ".gif", ".svg"}

var brandTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif", ".svg": "image/svg+xml",
}

var brandFileRe = regexp.MustCompile(`^(logo|logo_dark)\.(png|jpg|webp|gif|svg)$`)

var errBrandType = errors.New("only PNG, JPEG, WebP, GIF or SVG images can be uploaded")

// brandImageExt works out the extension from the file's bytes, never from its name or the browser's content type
func brandImageExt(b []byte) (string, error) {
	switch http.DetectContentType(b) {
	case "image/png":
		return ".png", nil
	case "image/jpeg":
		return ".jpg", nil
	case "image/webp":
		return ".webp", nil
	case "image/gif":
		return ".gif", nil
	}
	head := b
	if len(head) > 4096 {
		head = head[:4096]
	}
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	ct := http.DetectContentType(head)
	if (strings.HasPrefix(ct, "text/xml") || strings.HasPrefix(ct, "text/plain")) && bytes.Contains(bytes.ToLower(head), []byte("<svg")) {
		return ".svg", nil
	}
	return "", errBrandType
}

// saveBrandUpload stores the <key>_file upload, if there is one, and returns the url to save in the config
func saveBrandUpload(r *http.Request, key string) (string, error) {
	f, _, err := r.FormFile(key + "_file")
	if errors.Is(err, http.ErrMissingFile) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("the upload could not be read")
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, brandUploadMax+1))
	if err != nil {
		return "", errors.New("the upload could not be read")
	}
	if len(b) == 0 {
		return "", nil
	}
	if len(b) > brandUploadMax {
		return "", errors.New("the file is larger than 1 MB")
	}
	ext, err := brandImageExt(b)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(BrandDir, 0755); err != nil {
		return "", errors.New("the logo folder could not be created")
	}
	name := key + ext
	tmp, err := os.CreateTemp(BrandDir, ".upload-*")
	if err != nil {
		return "", errors.New("the file could not be saved")
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr == nil && cerr == nil {
		werr = os.Chmod(tmp.Name(), 0644)
	}
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return "", errors.New("the file could not be saved")
	}
	if err := os.Rename(tmp.Name(), filepath.Join(BrandDir, name)); err != nil {
		_ = os.Remove(tmp.Name())
		return "", errors.New("the file could not be saved")
	}
	// one file per logo, an older upload in another format would just sit there
	for _, e := range brandExts {
		if e != ext {
			_ = os.Remove(filepath.Join(BrandDir, key+e))
		}
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("/brand/%s?v=%s", name, hex.EncodeToString(sum[:6])), nil
}

// ServeBrandFile serves /brand/{file} so the logo previews on the settings page load, the user panel has the same route
func ServeBrandFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if !brandFileRe.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(BrandDir, name)
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", brandTypes[filepath.Ext(name)])
	h.Set("X-Content-Type-Options", "nosniff")
	// an svg opened on its own would run its scripts on this origin, sandbox stops that
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// removeBrandUploads deletes every uploaded file of one logo, logo never matches logo_dark.*
func removeBrandUploads(key string) {
	for _, e := range brandExts {
		_ = os.Remove(filepath.Join(BrandDir, key+e))
	}
}

var brandUploadLabels = map[string]string{"logo": "Logo", "logo_dark": "Logo for dark backgrounds"}
