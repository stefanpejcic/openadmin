// This file computes per-domain SSL certificate health for the /domains
// list page (expired / expiring soon / missing / unreadable cert file), in
// addition to the existing automatic/custom/none classification in
// domains.go (readCaddyFileForDomain), which is left untouched.
//
// The authoritative source for *where a custom certificate actually lives*
// is Caddy's own live admin API config (it resolves the tls_connection_policy
// -> certificate_selection tag -> certificates.load_files path for us,
// which is more trustworthy than guessing). That call is local but still a
// network round trip, so it gets a hard 5s timeout and the result is cached
// for 5 minutes so a slow/unreachable Caddy admin API can't slow down every
// page load. If the call fails or times out, domain cert paths are instead
// guessed from the on-disk naming convention opencli itself uses
// (/etc/openpanel/caddy/ssl/<custom|acme-v02.api.letsencrypt.org-directory>/<domain>/...).
//
// Either way, once a candidate certificate path is known, it's read and
// parsed locally (crypto/x509) to determine actual expiry -- Caddy's config
// dump never contains that, live or not.
package handlers

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// sslBaseDir is where opencli stores/mirrors certificate files on the host.
var sslBaseDir = "/etc/openpanel/caddy/ssl"

// caddyCertContainerPrefix is the path prefix Caddy's own config uses for
// certificate files (it runs with /etc/openpanel/caddy/ssl bind-mounted
// there). Custom-cert paths reported by the live admin API get this prefix
// swapped for sslBaseDir so they can be read from the host.
var caddyCertContainerPrefix = "/data/caddy/certificates/"

// caddyConfigAdminURL is Caddy's local admin API, scoped to just the "http"
// and "tls" apps (tls_connection_policies + automation policies +
// certificates.load_files -- everything needed to resolve cert paths).
var caddyConfigAdminURL = "http://localhost:2019/config/apps"

// caddyConfigTimeout bounds how long we'll wait on the admin API before
// falling back to the on-disk naming convention.
const caddyConfigTimeout = 5 * time.Second

// caddyConfigCacheTTL is how long a fetched (or failed) result is reused
// before the next /domains page load triggers a fresh admin API call.
const caddyConfigCacheTTL = 5 * time.Minute

// sslExpiringSoonWindow is how far out "expiring soon" looks.
const sslExpiringSoonWindow = 14 * 24 * time.Hour

// caddyConfigFetch performs the raw HTTP call. Injectable so tests never
// make a real request, matching the caddyFetchMetrics pattern in caddy.go.
var caddyConfigFetch = func() ([]byte, error) {
	client := &http.Client{Timeout: caddyConfigTimeout}
	resp, err := client.Get(caddyConfigAdminURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("caddy admin API returned status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// caddyAppsConfig is the subset of `GET /config/apps` this file cares about.
type caddyAppsConfig struct {
	HTTP struct {
		Servers map[string]struct {
			TLSConnectionPolicies []struct {
				Match []struct {
					SNI []string `json:"sni"`
				} `json:"match"`
				CertificateSelection struct {
					AnyTag []string `json:"any_tag"`
				} `json:"certificate_selection"`
			} `json:"tls_connection_policies"`
		} `json:"servers"`
	} `json:"http"`
	TLS struct {
		Automation struct {
			Policies []struct {
				OnDemand bool     `json:"on_demand"`
				Subjects []string `json:"subjects"`
			} `json:"policies"`
		} `json:"automation"`
		Certificates struct {
			LoadFiles []struct {
				Certificate string   `json:"certificate"`
				Tags        []string `json:"tags"`
			} `json:"load_files"`
		} `json:"certificates"`
	} `json:"tls"`
}

// caddyTLSIndex is the parsed, host-path-translated result of a config fetch.
type caddyTLSIndex struct {
	// customCertPaths maps a lowercased SNI subject (as it appears in the
	// live tls_connection_policies) to the host-readable certificate path.
	customCertPaths map[string]string
	ok              bool
}

func translateCaddyCertPath(containerPath string) string {
	if rest, cut := strings.CutPrefix(containerPath, caddyCertContainerPrefix); cut {
		return filepath.Join(sslBaseDir, rest)
	}
	return containerPath
}

// fetchCaddyTLSIndex does one live admin API call and builds the index.
// ok is false on any transport/parse failure, telling callers to fall back
// to the on-disk naming convention entirely.
func fetchCaddyTLSIndex() caddyTLSIndex {
	body, err := caddyConfigFetch()
	if err != nil {
		return caddyTLSIndex{ok: false}
	}

	var cfg caddyAppsConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return caddyTLSIndex{ok: false}
	}

	certByTag := map[string]string{}
	for _, lf := range cfg.TLS.Certificates.LoadFiles {
		for _, tag := range lf.Tags {
			certByTag[tag] = lf.Certificate
		}
	}

	customPaths := map[string]string{}
	for _, srv := range cfg.HTTP.Servers {
		for _, pol := range srv.TLSConnectionPolicies {
			if len(pol.CertificateSelection.AnyTag) == 0 {
				continue
			}
			certPath, ok := certByTag[pol.CertificateSelection.AnyTag[0]]
			if !ok {
				continue
			}
			hostPath := translateCaddyCertPath(certPath)
			for _, m := range pol.Match {
				for _, sni := range m.SNI {
					customPaths[strings.ToLower(sni)] = hostPath
				}
			}
		}
	}

	return caddyTLSIndex{customCertPaths: customPaths, ok: true}
}

var (
	caddyTLSIndexMu        sync.Mutex
	caddyTLSIndexCache     caddyTLSIndex
	caddyTLSIndexFetchedAt time.Time
)

// getCaddyTLSIndex returns the cached index, refetching from Caddy's admin
// API at most once per caddyConfigCacheTTL (a failed fetch is cached too, so
// an unreachable admin API doesn't get hit on every single page load).
func getCaddyTLSIndex() caddyTLSIndex {
	caddyTLSIndexMu.Lock()
	defer caddyTLSIndexMu.Unlock()

	if !caddyTLSIndexFetchedAt.IsZero() && time.Since(caddyTLSIndexFetchedAt) < caddyConfigCacheTTL {
		return caddyTLSIndexCache
	}

	caddyTLSIndexCache = fetchCaddyTLSIndex()
	caddyTLSIndexFetchedAt = time.Now()
	return caddyTLSIndexCache
}

// resetCaddyTLSIndexCache clears the cache so the next call refetches.
// Exists for tests.
func resetCaddyTLSIndexCache() {
	caddyTLSIndexMu.Lock()
	defer caddyTLSIndexMu.Unlock()
	caddyTLSIndexCache = caddyTLSIndex{}
	caddyTLSIndexFetchedAt = time.Time{}
}

// domainCertPath resolves the certificate file to check for a domain, given
// its automatic/custom/none classification (from readCaddyFileForDomain)
// and the (possibly unavailable) live Caddy TLS index. Returns "" for
// sslType "none" -- there's nothing to check.
func domainCertPath(domain, sslType string, idx caddyTLSIndex) string {
	switch sslType {
	case "custom":
		if idx.ok {
			if p, ok := idx.customCertPaths[strings.ToLower(domain)]; ok {
				return p
			}
		}
		return filepath.Join(sslBaseDir, "custom", domain, "fullchain.pem")
	case "automatic":
		// Caddy never surfaces on-demand-obtained cert paths through its
		// config (they're not static load_files entries), so this is
		// always the on-disk naming convention -- live or not.
		return filepath.Join(sslBaseDir, "acme-v02.api.letsencrypt.org-directory", domain, domain+".crt")
	default:
		return ""
	}
}

// domainSSLHealth is the outcome of checking one domain's certificate file.
type domainSSLHealth struct {
	// Status is "" (fine), "expiring", "expired", "missing", or
	// "unreadable".
	Status   string
	NotAfter time.Time // zero if unknown
}

// checkDomainSSLHealth reads and parses the resolved certificate file (the
// leaf certificate, i.e. the first PEM block) to determine expiry. sslType
// must be the value readCaddyFileForDomain already computed for this domain.
func checkDomainSSLHealth(domain, sslType string, idx caddyTLSIndex) domainSSLHealth {
	path := domainCertPath(domain, sslType, idx)
	if path == "" {
		return domainSSLHealth{}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return domainSSLHealth{Status: "missing"}
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return domainSSLHealth{Status: "unreadable"}
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return domainSSLHealth{Status: "unreadable"}
	}

	switch {
	case time.Now().After(cert.NotAfter):
		return domainSSLHealth{Status: "expired", NotAfter: cert.NotAfter}
	case time.Until(cert.NotAfter) <= sslExpiringSoonWindow:
		return domainSSLHealth{Status: "expiring", NotAfter: cert.NotAfter}
	default:
		return domainSSLHealth{NotAfter: cert.NotAfter}
	}
}
