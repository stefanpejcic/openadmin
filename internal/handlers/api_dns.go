// This file implements the JSON REST API's DNS routes: viewing/replacing a
// domain's raw BIND zone file, managing the DNS cluster's slave list and
// checking a slave's reachability, and viewing/replacing the default
// IPv4/IPv6 zone templates used for new domains. Each reuses the same
// on-disk paths and podman/rndc/ssh plumbing as its HTML admin-page
// equivalent (domains_dns_zones.go, dns_cluster.go, dns_templates.go) --
// only the response shape differs.
package handlers

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// APIDNS bundles the /api/domains/{domain}/dns, /api/dns/cluster* and
// /api/dns/zone-templates handlers.
type APIDNS struct {
	PublicIP string
}

// apiDecodeJSONBody checks the Content-Type and decodes the body in one
// step: a non-JSON Content-Type is rejected the same way as a JSON
// Content-Type whose body fails to parse -- both report ok=false, which
// every caller turns into the same 400 "Invalid JSON format".
func apiDecodeJSONBody(r *http.Request, v interface{}) bool {
	if !apiIsJSONContentType(r) {
		return false
	}
	return json.NewDecoder(r.Body).Decode(v) == nil
}

// ServeDomainDNSZone handles GET/POST /api/domains/{domain_name}/dns.
func (a *APIDNS) ServeDomainDNSZone(w http.ResponseWriter, r *http.Request) {
	domainName := r.PathValue("domain_name")
	if !isDomain(domainName) {
		writeJSONError(w, http.StatusBadRequest, "Invalid domain name.")
		return
	}
	bindFilePath := filepath.Join(BindZonesDir, domainName+".zone")

	if r.Method == http.MethodPost {
		var body map[string]interface{}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		contentVal, hasContent := body["content"]
		if !hasContent || contentVal == nil {
			writeJSONError(w, http.StatusBadRequest, "content is required")
			return
		}
		bindContent, isString := contentVal.(string)

		timestamp := dnsZoneBackupTimestamp()
		backupFilePath := filepath.Join(DNSZoneBackupDir, domainName+".zone.backup_"+timestamp)

		// Any failure in the backup/write/validate sequence below, including
		// a failed revert after a failed validation, is reported the same
		// way: a 500 with the failure text, never a bare crash. crashErr/
		// validationFailed/stderrOut model that block's three possible
		// outcomes.
		var stderrOut string
		var crashErr error
		validationFailed := false

		func() {
			if !isString {
				crashErr = fmt.Errorf("write() argument must be str")
				return
			}
			if fileExists(bindFilePath) {
				if err := copyFileWithMode(bindFilePath, backupFilePath); err != nil {
					crashErr = err
					return
				}
			}
			if err := os.WriteFile(bindFilePath, []byte(bindContent), 0644); err != nil {
				crashErr = err
				return
			}

			_, stderr, exitCode, verr := dnsZoneValidateRun(domainName, bindFilePath)
			if verr != nil {
				crashErr = verr
				return
			}
			if exitCode == 0 {
				dnsZoneReloadRun()
				if fileExists(backupFilePath) {
					os.Remove(backupFilePath)
				}
				return
			}

			stderrOut = stderr
			validationFailed = true
			if fileExists(backupFilePath) {
				if err := copyFileWithMode(backupFilePath, bindFilePath); err != nil {
					crashErr = err
				}
			}
		}()

		if crashErr != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": crashErr.Error()})
			return
		}
		if validationFailed {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("Zone file validation failed. Changes reverted. Error: %s", stderrOut),
			})
			return
		}
		writeJSON(w, map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Zone file for %s saved successfully and DNS service reloaded.", domainName),
		})
		return
	}

	if !fileExists(bindFilePath) {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Zone file for %s not found", domainName))
		return
	}
	content, err := os.ReadFile(bindFilePath)
	if err != nil {
		// Not caught by any try/except in the route this mirrors -- an
		// unhandled read failure here (e.g. a race with the file being
		// removed) falls through to a plain 500, not a JSON error body.
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"domain": domainName, "content": string(content)})
}

// ServeDNSCluster handles GET/POST /api/dns/cluster.
func (a *APIDNS) ServeDNSCluster(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body map[string]interface{}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		action, _ := body["action"].(string)

		switch action {
		case "enable", "disable":
			if err := updateDNSClusterConfigFile(DNSClusterConfigPath, action == "enable"); err != nil {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
					"success": false,
					"error":   fmt.Sprintf("Failed to %s DNS cluster: %s", action, err.Error()),
				})
				return
			}
			dnsRestartServiceRun()
			writeJSON(w, map[string]interface{}{"success": true, "message": fmt.Sprintf("DNS cluster %sd successfully.", action)})
			return

		case "create":
			ip, _ := body["ip"].(string)
			if ip == "" {
				writeJSONError(w, http.StatusBadRequest, "ip is required")
				return
			}
			parsed := net.ParseIP(ip)
			if parsed == nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid IP address format.")
				return
			}
			if parsed.To4() == nil {
				writeJSONError(w, http.StatusBadRequest, "Only IPv4 addresses are currently supported.")
				return
			}

			extracted, extractErr := extractDNSClusterConfig(DNSClusterConfigPath)
			if extractErr != nil {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
					"success": false,
					"error":   fmt.Sprintf("Failed to add IP to DNS cluster: %s", extractErr.Error()),
				})
				return
			}
			allIPs := map[string]bool{}
			for _, v := range append(append([]string{}, extracted.AllowTransfer...), extracted.AlsoNotify...) {
				allIPs[v] = true
			}
			if allIPs[ip] {
				writeJSONError(w, http.StatusBadRequest, "IP address already exists in configuration.")
				return
			}
			if !dnsSlaveReachableViaRNDC(ip) {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf(
					"Cannot reach %s via rndc. Ensure the slave has allow-new-zones yes, a matching rndc key, and controls block configured.", ip))
				return
			}
			if err := addIPToConfig(DNSClusterConfigPath, ip); err != nil {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
					"success": false,
					"error":   fmt.Sprintf("Failed to add IP to DNS cluster: %s", err.Error()),
				})
				return
			}
			dnsRestartServiceRun()
			go syncExistingZonesToSlave(ip, a.PublicIP)
			writeJSON(w, map[string]interface{}{"success": true, "message": fmt.Sprintf("IP %s added to DNS cluster successfully.", ip)})
			return

		case "delete":
			ip, _ := body["ip"].(string)
			if ip == "" {
				writeJSONError(w, http.StatusBadRequest, "ip is required")
				return
			}
			extracted, extractErr := extractDNSClusterConfig(DNSClusterConfigPath)
			if extractErr != nil {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
					"success": false,
					"error":   fmt.Sprintf("Failed to remove IP from DNS cluster: %s", extractErr.Error()),
				})
				return
			}
			allIPs := map[string]bool{}
			for _, v := range append(append([]string{}, extracted.AllowTransfer...), extracted.AlsoNotify...) {
				allIPs[v] = true
			}
			if !allIPs[ip] {
				writeJSONError(w, http.StatusNotFound, fmt.Sprintf("IP %s not found in DNS cluster configuration.", ip))
				return
			}
			if err := removeIPFromConfig(DNSClusterConfigPath, ip); err != nil {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
					"success": false,
					"error":   fmt.Sprintf("Failed to remove IP from DNS cluster: %s", err.Error()),
				})
				return
			}
			dnsRestartServiceRun()
			writeJSON(w, map[string]interface{}{"success": true, "message": fmt.Sprintf("IP %s removed from DNS cluster successfully.", ip)})
			return

		default:
			writeJSONError(w, http.StatusBadRequest, "Invalid action. Use enable, disable, create, or delete.")
			return
		}
	}

	extracted, err := extractDNSClusterConfig(DNSClusterConfigPath)
	if err != nil {
		// Still a 200: the route this mirrors only ever wraps the read in a
		// try/except that swaps in an {"error": ...} dict, it never changes
		// the response status.
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{
		"allow_transfer": nonNilStrings(extracted.AllowTransfer),
		"also_notify":    nonNilStrings(extracted.AlsoNotify),
		"enabled":        extracted.Enabled,
		"raw_content":    extracted.RawContent,
	})
}

// ServeDNSClusterNodeInfo handles GET /api/dns/cluster/{ip}.
func (a *APIDNS) ServeDNSClusterNodeInfo(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	result := map[string]interface{}{"ip": ip, "status": "failed", "output": nil, "error": nil}

	ok, output := dnsRNDCCommandRun(ip, "status")
	if ok && strings.Contains(output, "number of zones") {
		result["status"] = "success"
		result["output"] = strings.TrimSpace(output)
		result["method"] = "rndc"
		writeJSON(w, result)
		return
	}

	code, stdout, stderr, timedOut, err := dnsSSHRun(ip, 5*time.Second, "uname -a")
	if timedOut {
		result["status"] = "timeout"
		result["error"] = "Connection timed out."
		writeJSON(w, result)
		return
	}
	if err != nil {
		result["status"] = "error"
		result["error"] = err.Error()
		writeJSON(w, result)
		return
	}

	if code == 0 {
		result["status"] = "success"
		result["method"] = "ssh"
	} else {
		result["status"] = "error"
	}
	if strings.TrimSpace(stdout) != "" {
		result["output"] = strings.TrimSpace(stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		result["error"] = strings.TrimSpace(stderr)
	}
	writeJSON(w, result)
}

// ServeDNSZoneTemplates handles GET/POST /api/dns/zone-templates.
func (a *APIDNS) ServeDNSZoneTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body map[string]interface{}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}

		for _, item := range []struct {
			key  string
			path string
		}{
			{"zone_template_ipv4", DNSZoneTemplateIPv4Path},
			{"zone_template_ipv6", DNSZoneTemplateIPv6Path},
		} {
			v, present := body[item.key]
			if !present || v == nil {
				continue
			}
			s, isString := v.(string)
			if !isString {
				// write_file()'s f.write(value) requires a str; a non-string
				// value would raise a TypeError here that nothing in this
				// route catches, so this crashes out the same way.
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			os.WriteFile(item.path, []byte(s), 0644)
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": "Template updated successfully!"})
		return
	}

	ipv4, err := readFileOrEmpty(DNSZoneTemplateIPv4Path)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	ipv6, err := readFileOrEmpty(DNSZoneTemplateIPv6Path)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{
		"zone_template_ipv4": ipv4,
		"zone_template_ipv6": ipv6,
	})
}
