// This file implements the large OpenPanel (end-user panel) configuration
// form -- branding, MySQL limits, file manager limits, session/login
// limits, and various yes/no toggles.
package handlers

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// OpenpanelSettings bundles the /settings/open-panel handler.
type OpenpanelSettings struct {
	Sessions *auth.Manager
}

var (
	OpenpanelSettingsConfigPath      = "/etc/openpanel/openpanel/conf/openpanel.config"
	OpenpanelSettingsRestartFlagPath = "/root/openpanel_restart_needed"
)

// openpanelIntFields lists the ~20 keys that must parse as a non-negative
// integer before being stored. A missing or non-numeric value for any of
// these is reported as a graceful validation error rather than being
// silently accepted. Only reachable via a malformed direct POST (the
// real HTML form always submits every field with a default), but
// handled deliberately rather than left to fail unpredictably.
var openpanelIntFields = []string{
	"autopurge_trash",
	"mysql_startup_time", "mysql_import_max_size_gb",
	"filemanager_edit_size", "filemanager_view_size", "filemanager_download_size",
	"filemanager_upload_size", "filemanager_compress_max_time", "filemanager_download_max_time",
	"filemanager_extract_max_time",
	"max_login_records", "login_ratelimit", "login_blocklimit",
	"session_duration", "session_lifetime", "activity_items_per_page",
	"domains_per_page", "terminal_timeout", "resource_usage_retention",
	"resource_usage_items_per_page",
}

// openpanelStringFields lists the plain-string fields (those not in
// openpanelIntFields above, and not the "weakpass" checkbox).
var openpanelStringFields = []string{
	"brand_name", "logo", "favicon", "ns1", "ns2", "ns3", "ns4",
	"avatar_type", "resource_usage_charts_mode", "password_reset", "password_strength",
	"permit_username_change_by_user", "permit_subdomain_sharing",
	"twofa_nag", "twofa_enforce", "how_to_guides", "found_a_bug_link", "ip_county_flag",
	"mysql_restricted_usernames", "mysql_restricted_databases",
	"filemanager_buttons_style", "filemanager_edit_extensions",
	"filemanager_image_extensions", "filemanager_archives_extensions",
	"logout_url",
}

type openpanelValidationKind int

const (
	openpanelEnum openpanelValidationKind = iota
	openpanelNonNegativeInt
	openpanelOneToHundred
	openpanelSpaceSeparatedList
	openpanelSpaceSeparatedExtensions
)

type openpanelRule struct {
	kind    openpanelValidationKind
	options []string
}

var openpanelValidValues = map[string]openpanelRule{
	"avatar_type":                     {kind: openpanelEnum, options: []string{"gravatar", "icon", "letter"}},
	"resource_usage_charts_mode":      {kind: openpanelEnum, options: []string{"one", "two", "none"}},
	"activity_items_per_page":         {kind: openpanelNonNegativeInt},
	"login_ratelimit":                 {kind: openpanelNonNegativeInt},
	"login_blocklimit":                {kind: openpanelNonNegativeInt},
	"session_duration":                {kind: openpanelNonNegativeInt},
	"session_lifetime":                {kind: openpanelNonNegativeInt},
	"resource_usage_items_per_page":   {kind: openpanelNonNegativeInt},
	"resource_usage_retention":        {kind: openpanelNonNegativeInt},
	"max_login_records":               {kind: openpanelNonNegativeInt},
	"domains_per_page":                {kind: openpanelNonNegativeInt},
	"terminal_timeout":                {kind: openpanelNonNegativeInt},
	"autopurge_trash":                 {kind: openpanelNonNegativeInt},
	"mysql_startup_time":              {kind: openpanelNonNegativeInt},
	"mysql_import_max_size_gb":        {kind: openpanelNonNegativeInt},
	"mysql_restricted_usernames":      {kind: openpanelSpaceSeparatedList},
	"mysql_restricted_databases":      {kind: openpanelSpaceSeparatedList},
	"filemanager_buttons_style":       {kind: openpanelEnum, options: []string{"classic", "modern"}},
	"filemanager_edit_size":           {kind: openpanelNonNegativeInt},
	"filemanager_view_size":           {kind: openpanelNonNegativeInt},
	"filemanager_download_size":       {kind: openpanelNonNegativeInt},
	"filemanager_upload_size":         {kind: openpanelNonNegativeInt},
	"filemanager_compress_max_time":   {kind: openpanelNonNegativeInt},
	"filemanager_extract_max_time":    {kind: openpanelNonNegativeInt},
	"filemanager_download_max_time":   {kind: openpanelNonNegativeInt},
	"filemanager_edit_extensions":     {kind: openpanelSpaceSeparatedExtensions},
	"filemanager_image_extensions":    {kind: openpanelSpaceSeparatedExtensions},
	"filemanager_archives_extensions": {kind: openpanelSpaceSeparatedExtensions},
	"how_to_guides":                   {kind: openpanelEnum, options: []string{"yes", "no"}},
	"twofa_nag":                       {kind: openpanelEnum, options: []string{"yes", "no"}},
	"twofa_enforce":                   {kind: openpanelEnum, options: []string{"yes", "no"}},
	"found_a_bug_link":                {kind: openpanelEnum, options: []string{"yes", "no"}},
	"ip_county_flag":                  {kind: openpanelEnum, options: []string{"yes", "no"}},
	"password_reset":                  {kind: openpanelEnum, options: []string{"yes", "no"}},
	"password_strength":               {kind: openpanelOneToHundred},
	"permit_subdomain_sharing":        {kind: openpanelEnum, options: []string{"yes", "no"}},
	"permit_username_change_by_user":  {kind: openpanelEnum, options: []string{"yes", "no"}},
}

var openpanelSpaceSeparatedListRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// validateOpenpanelValue validates value against the rule registered for
// key. Returns the (possibly normalized) value to store and whether it
// passed; on failure the caller should append errMsg to the error list
// and leave the existing config value untouched.
func validateOpenpanelValue(key, value string) (normalized string, ok bool, errMsg string) {
	rule, hasRule := openpanelValidValues[key]
	if !hasRule {
		return value, true, ""
	}

	switch rule.kind {
	case openpanelNonNegativeInt:
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return "", false, fmt.Sprintf("Error: '%s' must be a non-negative integer for %s.", value, key)
		}
		return strconv.Itoa(n), true, ""

	case openpanelOneToHundred:
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 100 {
			return "", false, fmt.Sprintf("Error: '%s' must be an integer between 1 and 100 for %s.", value, key)
		}
		return strconv.Itoa(n), true, ""

	case openpanelSpaceSeparatedList:
		items := strings.Fields(value)
		for _, item := range items {
			if !openpanelSpaceSeparatedListRe.MatchString(item) {
				return "", false, fmt.Sprintf("Error: '%s' must be a space-separated list of valid names for %s.", value, key)
			}
		}
		return strings.Join(items, " "), true, ""

	case openpanelSpaceSeparatedExtensions:
		extensions := strings.Fields(value)
		for _, ext := range extensions {
			if !strings.HasPrefix(ext, ".") && !isAlpha(ext) {
				return "", false, fmt.Sprintf("Error: '%s' must be space-separated valid file extensions for %s.", value, key)
			}
		}
		return strings.Join(extensions, " "), true, ""

	default: // enum
		for _, opt := range rule.options {
			if value == opt {
				return value, true, ""
			}
		}
		return "", false, fmt.Sprintf("Error: '%s' is not a valid value for %s.", value, key)
	}
}

func isAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

// openpanelSectionForKey determines which ini section a given key
// belongs to.
func openpanelSectionForKey(key string) string {
	switch key {
	case "brand_name", "logo", "favicon", "ns1", "ns2", "ns3", "ns4", "logout_url":
		return "DEFAULT"
	}
	if strings.HasPrefix(key, "mysql_") {
		return "DATABASES"
	}
	if strings.HasPrefix(key, "filemanager_") || key == "autopurge_trash" {
		return "FILES"
	}
	if key == "terminal_timeout" {
		return "PANEL"
	}
	return "USERS"
}

// loadOpenpanelConfigStripped is a separate ini parser used only for the
// final render, distinct from config.Load() (used for the POST-merge
// base above) -- config.Load() does NOT strip quotes from values, while
// this one does; the two parsing paths are intentionally different.
func loadOpenpanelConfigStripped(path string) config.Data {
	data := config.Data{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return data
	}
	section := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[]")
			continue
		}
		if line == "" || !strings.Contains(line, "=") || section == "" {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		if data[section] == nil {
			data[section] = map[string]string{}
		}
		data[section][key] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return data
}

// ServeOpenpanelSettings handles GET/POST /settings/open-panel.
func (o *OpenpanelSettings) ServeOpenpanelSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()

		configData := config.Load(OpenpanelSettingsConfigPath)
		var successMessages, errorMessages []string
		restartNeeded := false

		// The ~20 numeric fields. A missing or non-numeric value is
		// reported as a graceful validation error (existing config value
		// left untouched) rather than aborting the request; a
		// numeric-but-negative value is rejected the same way.
		for _, key := range openpanelIntFields {
			raw := r.PostFormValue(key)
			n, err := strconv.Atoi(raw)
			if err != nil {
				errorMessages = append(errorMessages, fmt.Sprintf("Error: '%s' must be a non-negative integer for %s.", raw, key))
				continue
			}
			if n < 0 {
				errorMessages = append(errorMessages, fmt.Sprintf("Error: '%d' must be a non-negative integer for %s.", n, key))
				continue
			}
			configData.Set(openpanelSectionForKey(key), key, strconv.Itoa(n))
		}

		// "weakpass" is a checkbox, so its presence indicates the enabled
		// state rather than a submitted string value. It's converted here
		// to the same "yes"/"no" string convention as every other enum
		// toggle on this page, so it validates correctly against the enum
		// rule for weakpass and is actually stored.
		weakpassValue := "no"
		if formHasKey(r, "weakpass") {
			weakpassValue = "yes"
		}
		configData.Set(openpanelSectionForKey("weakpass"), "weakpass", weakpassValue)

		for _, key := range openpanelStringFields {
			if !formHasKey(r, key) {
				continue
			}
			raw := r.PostFormValue(key)
			value, ok, errMsg := validateOpenpanelValue(key, raw)
			if !ok {
				errorMessages = append(errorMessages, errMsg)
				continue
			}
			configData.Set(openpanelSectionForKey(key), key, value)
		}

		// config.Save() fully regenerates the file from the parsed config
		// data (unquoted `key=value` lines, comments lost) rather than
		// patching individual lines in place. It adds a blank line between
		// sections; this is cosmetic and doesn't affect re-parsing, since
		// blank lines are skipped by every reader of this file.
		if err := config.Save(OpenpanelSettingsConfigPath, configData); err != nil {
			errorMessages = append(errorMessages, "Error saving configuration file.")
		} else {
			restartNeeded = true
			successMessages = append(successMessages, "Configuration saved successfully.")
		}

		if restartNeeded {
			os.WriteFile(OpenpanelSettingsRestartFlagPath, []byte("Restart needed for OpenPanel service."), 0644)
		}

		for _, m := range successMessages {
			auth.AddFlash(w, r, o.Sessions, m, "success")
		}
		for _, m := range errorMessages {
			auth.AddFlash(w, r, o.Sessions, m, "error")
		}
	}

	// Always a fresh disk re-read for the final render, ignoring whatever
	// was just computed above during POST.
	configData := loadOpenpanelConfigStripped(OpenpanelSettingsConfigPath)

	webtemplates.Render(w, "settings_openpanel.html", mergeChrome(map[string]interface{}{
		"ConfigData": configData,
		"CSRFToken":  csrf.Token(r),
		"Flashes":    auth.PopFlashes(w, r, o.Sessions),
	}, r, "OpenPanel Settings"))
}
