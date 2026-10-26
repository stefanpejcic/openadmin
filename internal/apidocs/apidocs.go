// Package apidocs embeds api_endpoints.json, the same source openapi.yaml is generated from, and renders it as the plain-text list behind /settings/api/endpoints and `opencli api list`.
package apidocs

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed api_endpoints.json
var raw []byte

type endpoint struct {
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Auth        string          `json:"auth"`
	Description string          `json:"description"`
	Params      string          `json:"params"`
	Body        json.RawMessage `json:"body"`
}

type group struct {
	Group     string     `json:"group"`
	Endpoints []endpoint `json:"endpoints"`
}

// same wording as scripts/generate_openapi.py
var authText = map[string]string{
	"none":                      "No authentication.",
	"RequireAPIToken":           "Any authenticated user.",
	"RequireAPIAdmin":           "Administrator or user role (the reseller role is blocked).",
	"RequireAPIOwnerOrAdmin":    "Administrator/user role, or a reseller who owns the target account.",
	"RequireAPIAdminOrReseller": "Administrator/user role, or a reseller limited to the plans assigned to them.",
	"RequireAPISuperAdmin":      "The Super Admin only.",
}

const separator = "--------------------------------------------------------------------------------"

// Text renders every endpoint with a curl example against baseURL, e.g. "https://server.example.com:2087"
func Text(baseURL string) (string, error) {
	var groups []group
	if err := json.Unmarshal(raw, &groups); err != nil {
		return "", fmt.Errorf("parse api_endpoints.json: %w", err)
	}
	baseURL = strings.TrimRight(baseURL, "/")

	var b strings.Builder
	for _, g := range groups {
		fmt.Fprintf(&b, "# %s\n\n", g.Group)
		for _, e := range g.Endpoints {
			fmt.Fprintf(&b, "Endpoint: %s %s\n", e.Method, e.Path)
			fmt.Fprintf(&b, "Description: %s\n", e.Description)
			if t, ok := authText[e.Auth]; ok {
				fmt.Fprintf(&b, "Access: %s\n", t)
			}
			fmt.Fprintf(&b, "Example:\n  %s\n\n%s\n\n", curl(baseURL, e), separator)
		}
	}
	return b.String(), nil
}

func curl(baseURL string, e endpoint) string {
	cmd := fmt.Sprintf("curl -X %s %s", e.Method, shellQuote(baseURL+e.Path+e.Params))
	if e.Auth != "none" {
		cmd += ` -H "Authorization: Bearer TOKEN_HERE"`
	}
	if len(e.Body) > 0 && string(e.Body) != "null" {
		cmd += ` -H "Content-Type: application/json" -d ` + shellQuote(compact(e.Body))
	}
	return cmd
}

func compact(m json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, m); err != nil {
		return string(m)
	}
	return buf.String()
}

// so a ' in a value doesn't end the single-quoted shell argument
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
