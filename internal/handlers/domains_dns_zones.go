// This file implements the BIND zone file editor at /domains/dns and
// /domains/dns/<domain_name>.
package handlers

import (
	"bytes"
	"database/sql"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// DNSZoneEditor bundles the /domains/dns handlers.
type DNSZoneEditor struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// BindZonesDir is the BIND zone files directory (/etc/bind/zones).
var BindZonesDir = "/etc/bind/zones"

// DNSZoneBackupDir is the /tmp prefix used for the zone file's timestamped
// backups and their glob pattern.
var DNSZoneBackupDir = "/tmp"

// podmanExecCapture runs a podman-exec-style command against context and
// captures its stdout/stderr and exit code: a nonzero exit code is
// reported via exitCode, with err staying nil, since a nonzero exit is an
// expected outcome the caller inspects -- not a failure to run the
// command. A genuine failure to run the command at all (e.g. the binary
// isn't installed) is reported via err instead. Shared by the DNS zone
// editor and the Caddy file editor below.
func podmanExecCapture(context string, args ...string) (stdout, stderr string, exitCode int, err error) {
	cmd, cmdErr := podman.Command(context, args...)
	if cmdErr != nil {
		return "", "", 0, cmdErr
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), 0, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// podmanFireAndForgetRun runs a podman-exec command and reports only
// whether the command could actually be started/run at all -- a nonzero
// exit code is deliberately ignored (err stays nil), since callers never
// inspect it. A real failure to execute (e.g. missing binary) does
// propagate.
func podmanFireAndForgetRun(context string, args ...string) error {
	cmd, err := podman.Command(context, args...)
	if err != nil {
		return err
	}
	if runErr := cmd.Run(); runErr != nil {
		if _, ok := runErr.(*exec.ExitError); ok {
			return nil
		}
		return runErr
	}
	return nil
}

// fileExists reports whether path exists (an unreadable parent directory
// reads the same as "not present" for this file's purposes).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// copyFileWithMode copies file content and permission bits from src to
// dst.
func copyFileWithMode(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	mode := os.FileMode(0644)
	if info, statErr := os.Stat(src); statErr == nil {
		mode = info.Mode()
	}
	return os.WriteFile(dst, data, mode)
}

// latestBackupFile returns the lexicographically-last match for pattern,
// which is the newest backup since the filename suffix is a
// YYYYMMDDHHMMSS timestamp.
func latestBackupFile(pattern string) (string, bool) {
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return "", false
	}
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	return matches[0], true
}

// dnsZoneValidateRun is injectable so tests never shell out to a real
// podman/named-checkzone binary.
var dnsZoneValidateRun = func(domainName, zonePath string) (stdout, stderr string, exitCode int, err error) {
	return podmanExecCapture("default", "exec", "openpanel_dns", "named-checkzone", domainName, zonePath)
}

// dnsZoneReloadRun is injectable; issues a fire-and-forget podman exec to
// reload the DNS service via rndc. Its return value is intentionally not
// checked by callers.
var dnsZoneReloadRun = func() error {
	return podmanFireAndForgetRun("default", "exec", "openpanel_dns", "rndc", "reload")
}

// ServeEditDNSZone handles GET /domains/dns, GET /domains/dns/{domain_name}
// and POST /domains/dns/{domain_name}.
func (h *DNSZoneEditor) ServeEditDNSZone(w http.ResponseWriter, r *http.Request) {
	domainName := r.PathValue("domain_name")

	if domainName == "" {
		domains, err := paneldb.GetAllDomains(h.MySQL)
		mysqlIsDown := err != nil
		if mysqlIsDown {
			domains = nil
		}
		webtemplates.Render(w, "domains_dns_zone_editor.html", mergeChrome(map[string]interface{}{
			"Domains":     domains,
			"MySQLIsDown": mysqlIsDown,
			"DomainName":  "",
			"CSRFToken":   csrf.Token(r),
			"Flashes":     auth.PopFlashes(w, r, h.Sessions),
		}, r, "DNS Zone Editor"))
		return
	}

	if !isDomain(domainName) {
		auth.AddFlash(w, r, h.Sessions, "Invalid domain name format.", "danger")
		http.Redirect(w, r, "/domains/dns", http.StatusSeeOther)
		return
	}

	bindFilePath := filepath.Join(BindZonesDir, domainName+".zone")
	backupGlobPattern := filepath.Join(DNSZoneBackupDir, domainName+".zone.backup_*")

	var tmpFileContent string
	var hasTmpFileContent bool
	if latest, ok := latestBackupFile(backupGlobPattern); ok {
		if content, err := os.ReadFile(latest); err == nil {
			tmpFileContent = string(content)
			hasTmpFileContent = true
		}
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		bindContentForm := r.PostFormValue("bind_content")
		timestamp := dnsZoneBackupTimestamp()
		backupFilePath := filepath.Join(DNSZoneBackupDir, domainName+".zone.backup_"+timestamp)

		procErr := func() error {
			if fileExists(bindFilePath) {
				if err := copyFileWithMode(bindFilePath, backupFilePath); err != nil {
					return err
				}
			}
			if err := os.WriteFile(bindFilePath, []byte(bindContentForm), 0644); err != nil {
				return err
			}

			zonePath := filepath.Join(BindZonesDir, domainName+".zone")
			_, stderr, exitCode, verr := dnsZoneValidateRun(domainName, zonePath)
			if verr != nil {
				return verr
			}
			if exitCode == 0 {
				if rerr := dnsZoneReloadRun(); rerr != nil {
					return rerr
				}
				auth.AddFlash(w, r, h.Sessions, "Zone file for "+domainName+" saved successfully and DNS service reloaded.", "success")
				if fileExists(backupFilePath) {
					if err := os.Remove(backupFilePath); err != nil {
						return err
					}
				}
				return nil
			}

			// Validation failed: revert.
			if fileExists(backupFilePath) {
				if err := copyFileWithMode(backupFilePath, bindFilePath); err != nil {
					return err
				}
			}
			auth.AddFlash(w, r, h.Sessions, "Zone file validation failed. Changes reverted. Error: "+stderr, "error")
			return nil
		}()

		if procErr != nil {
			// The "restored" flash below fires whether or not a backup
			// actually existed to restore from (the restore attempt is
			// itself gated on existence, but the flash isn't) -- a real
			// quirk, kept as-is rather than tightened.
			var copyErr error
			if fileExists(backupFilePath) {
				copyErr = copyFileWithMode(backupFilePath, bindFilePath)
			}
			if copyErr == nil {
				auth.AddFlash(w, r, h.Sessions, "Error occurred, original zone file restored. Exception: "+procErr.Error(), "error")
			} else {
				auth.AddFlash(w, r, h.Sessions, "Critical error: could not restore backup! Exception: "+copyErr.Error(), "error")
			}
		}
	}

	var bindContent string
	if fileExists(bindFilePath) {
		content, err := os.ReadFile(bindFilePath)
		if err == nil {
			bindContent = string(content)
		}
	} else {
		auth.AddFlash(w, r, h.Sessions, "Error reading DNS zone file for "+domainName+".", "error")
		bindContent = ""
	}

	webtemplates.Render(w, "domains_dns_zone_editor.html", mergeChrome(map[string]interface{}{
		"Domains":           nil,
		"DomainName":        domainName,
		"BindContent":       bindContent,
		"TmpFileContent":    tmpFileContent,
		"HasTmpFileContent": hasTmpFileContent,
		"CSRFToken":         csrf.Token(r),
		"Flashes":           auth.PopFlashes(w, r, h.Sessions),
	}, r, "DNS Zone Editor"))
}

// defaultBackupTimestamp formats the current time as YYYYMMDDHHMMSS;
// shared by the DNS zone editor and the Caddy file editor.
func defaultBackupTimestamp() string {
	return time.Now().Format("20060102150405")
}

// dnsZoneBackupTimestampRun is injectable so tests can assert on a fixed
// backup filename.
var dnsZoneBackupTimestampRun = defaultBackupTimestamp

func dnsZoneBackupTimestamp() string { return dnsZoneBackupTimestampRun() }
