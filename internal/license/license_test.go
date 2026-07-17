package license

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestType(t *testing.T) {
	cases := map[string]string{
		"enterprise-XXXX-YYYY": "Enterprise",
		"noc-XXXX":             "Enterprise",
		"lifetime-XXXX":        "Enterprise",
		"":                     "Community",
		"community":            "Community",
		"trial-XXXX":           "Community",
	}
	for key, want := range cases {
		if got := Type(key); got != want {
			t.Errorf("Type(%q) = %q, want %q", key, got, want)
		}
	}
}

func withScratchCache(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig := CacheFilePath
	CacheFilePath = filepath.Join(dir, "license_cache.json")
	t.Cleanup(func() { CacheFilePath = orig })
}

func withMockAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	orig := APIURL
	APIURL = srv.URL
	t.Cleanup(func() { APIURL = orig })
}

func TestCheckerValidWhenAPIReturnsActive(t *testing.T) {
	withScratchCache(t)
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<response><status>Active</status><description>ok</description></response>")
	})

	c := NewChecker("enterprise-test-key", "203.0.113.1")
	if !c.Valid() {
		t.Fatal("expected license to be valid when the API reports Active")
	}

	if _, err := os.Stat(CacheFilePath); err != nil {
		t.Fatalf("expected a successful check to write the cache file: %v", err)
	}
}

func TestCheckerInvalidWhenAPIReportsInactive(t *testing.T) {
	withScratchCache(t)
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<response><status>Expired</status></response>")
	})

	c := NewChecker("enterprise-test-key", "203.0.113.1")
	if c.Valid() {
		t.Fatal("expected license to be invalid when the API reports a non-Active status")
	}
}

func TestCheckerFallsBackToFreshCacheOnRemoteFailure(t *testing.T) {
	withScratchCache(t)

	// seed a cache file as if a successful check happened moments ago
	writeCache()

	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	c := NewChecker("enterprise-test-key", "203.0.113.1")
	if !c.Valid() {
		t.Fatal("expected grace-period fallback to a fresh cache to keep the license valid")
	}
}

func TestCheckerFailsClosedOnStaleCache(t *testing.T) {
	withScratchCache(t)

	// seed a cache file with a timestamp well outside the 24h grace period
	stale := cacheFile{CheckedAt: float64(time.Now().Add(-48 * time.Hour).Unix())}
	writeStaleCache(t, stale)

	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	c := NewChecker("enterprise-test-key", "203.0.113.1")
	if c.Valid() {
		t.Fatal("expected a stale (>24h) cache to fail closed, not keep the license valid")
	}
}

func TestRequireEnterpriseGate(t *testing.T) {
	withScratchCache(t)
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<response><status>Active</status></response>")
	})
	valid := NewChecker("enterprise-test-key", "203.0.113.1")

	invalid := &Checker{} // zero value -- never checked, Valid() reports false

	handler := RequireEnterprise(valid, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected a valid Enterprise license to pass the gate, got %d", rec.Code)
	}

	handler = RequireEnterprise(invalid, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected an invalid license to be blocked with 403, got %d", rec.Code)
	}

	handler = RequireEnterprise(nil, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected a nil checker (Community edition, no checker configured) to be blocked with 403, got %d", rec.Code)
	}
}

func writeStaleCache(t *testing.T, c cacheFile) {
	t.Helper()
	f, err := os.Create(CacheFilePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintf(f, `{"checked_at": %f}`, c.CheckedAt)
}
