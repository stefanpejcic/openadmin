// This file implements the clustering default-node config page (SSH
// connection validation + admin.ini [CLUSTERING] section).
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"os/exec"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// Slave bundles the /server/node handler.
type Slave struct {
	Sessions *auth.Manager
}

// slaveValidateSSHConnection runs a BatchMode, non-interactive "echo ok"
// over ssh with a 5s connect timeout and an overall 8s command timeout.
// Distinguishes "ran but failed" (returns stderr) from "couldn't even
// run/timed out" (returns the error text itself) -- these intentionally
// produce differently-shaped messages rather than being normalized to
// one form.
var slaveValidateSSHConnection = func(node, sshKeyPath string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ssh",
		"-i", sshKeyPath,
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "PasswordAuthentication=no",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=accept-new",
		node,
		"echo ok",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		if strings.Contains(stdout.String(), "ok") {
			return true, strings.TrimSpace(stdout.String())
		}
		return false, strings.TrimSpace(stderr.String())
	}
	if _, isExit := err.(*exec.ExitError); isExit {
		return false, strings.TrimSpace(stderr.String())
	}
	return false, err.Error()
}

// ServeNode handles GET/POST /server/node.
func (s *Slave) ServeNode(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()

		nodeValue := r.PostFormValue("default_node")
		keyValue := r.PostFormValue("default_ssh_key_path")
		if nodeValue != "" && keyValue != "" {
			valid, sshErr := slaveValidateSSHConnection(nodeValue, keyValue)
			if !valid {
				auth.AddFlash(w, r, s.Sessions, "SSH validation failed: "+sshErr, "error")
				http.Redirect(w, r, "/server/node", http.StatusSeeOther)
				return
			}
		}

		data := config.Load(config.AdminConfigPath)
		if formHasKey(r, "default_node") {
			data.Set("CLUSTERING", "default_node", r.PostFormValue("default_node"))
		}
		if formHasKey(r, "default_ssh_key_path") {
			data.Set("CLUSTERING", "default_ssh_key_path", r.PostFormValue("default_ssh_key_path"))
		}

		if err := config.Save(config.AdminConfigPath, data); err != nil {
			auth.AddFlash(w, r, s.Sessions, "Failed to write config: "+err.Error(), "error")
		} else {
			auth.AddFlash(w, r, s.Sessions, "Default node edited successfully.", "success")
		}
		http.Redirect(w, r, "/server/node", http.StatusSeeOther)
		return
	}

	data := config.Load(config.AdminConfigPath)
	defaultNodeRaw := data.Get("CLUSTERING", "default_node", "")
	defaultSSHKeyPathRaw := data.Get("CLUSTERING", "default_ssh_key_path", "")

	var sshValid *bool
	var sshError string
	// Validation (and the JSON response below) use the quote-stripped
	// values, but the HTML template below is handed the RAW, unstripped
	// values. This inconsistency is kept as-is rather than unified.
	defaultNodeStripped := strings.Trim(defaultNodeRaw, `"`)
	defaultSSHKeyPathStripped := strings.Trim(defaultSSHKeyPathRaw, `"`)
	if defaultNodeRaw != "" && defaultSSHKeyPathRaw != "" {
		v, e := slaveValidateSSHConnection(defaultNodeStripped, defaultSSHKeyPathStripped)
		sshValid = &v
		sshError = e
	}

	if r.URL.Query().Get("output") == "json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"default_node":         defaultNodeStripped,
			"default_ssh_key_path": defaultSSHKeyPathStripped,
			"ssh_valid":            sshValid,
			"ssh_error":            sshError,
		})
		return
	}

	webtemplates.Render(w, "server_node.html", mergeChrome(map[string]interface{}{
		"DefaultNode":       defaultNodeRaw,
		"DefaultSSHKeyPath": defaultSSHKeyPathRaw,
		"SSHValid":          sshValid,
		"SSHError":          sshError,
		"CSRFToken":         csrf.Token(r),
		"Flashes":           auth.PopFlashes(w, r, s.Sessions),
	}, r, "Clustering Default Node"))
}
