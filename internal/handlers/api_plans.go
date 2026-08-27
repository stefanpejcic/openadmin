// This file implements the JSON REST API's plan-management surface:
// GET/POST /api/plans and GET/PUT/PATCH/DELETE /api/plans/{plan_id}. Unlike
// every other file in this package, these two routes carry no role gate at
// all beyond having a valid token -- any authenticated admin, user, or
// reseller may list, create, edit, or delete plans through this API.
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"openadmin/internal/paneldb"
)

// APIPlans bundles the /api/plans handlers.
type APIPlans struct {
	MySQL *sql.DB
}

// jsonStringOr returns data[key] formatted as a string, or fallback if the
// key is absent or null.
func jsonStringOr(data map[string]interface{}, key, fallback string) string {
	v, ok := data[key]
	if !ok || v == nil {
		return fallback
	}
	if s, ok := v.(string); ok {
		return s
	}
	return jsonScalarToString(v)
}

// jsonTruthyStringOr is like jsonStringOr, but also falls back for a key
// that's present but holds a falsy JSON value (empty string, zero, or
// false), not just a missing/null one.
func jsonTruthyStringOr(data map[string]interface{}, key, fallback string) string {
	v, ok := data[key]
	if !ok || v == nil {
		return fallback
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return fallback
		}
		return t
	case float64:
		if t == 0 {
			return fallback
		}
		return jsonScalarToString(t)
	case bool:
		if !t {
			return fallback
		}
		return jsonScalarToString(t)
	default:
		return fallback
	}
}

// jsonStringOrNone mirrors an f-string interpolation of a possibly-missing
// dict key with no default: a present value renders as its string form, but
// an absent or null key renders as the literal text "None" rather than an
// empty string.
func jsonStringOrNone(data map[string]interface{}, key string) string {
	v, ok := data[key]
	if !ok || v == nil {
		return "None"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return jsonScalarToString(v)
}

func jsonScalarToString(v interface{}) string {
	switch t := v.(type) {
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "True"
		}
		return "False"
	default:
		return ""
	}
}

// ServeList handles GET/POST /api/plans. Wrap with (*APIAuth).RequireAPIToken.
func (p *APIPlans) ServeList(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		p.handleCreate(w, r)
		return
	}

	// Reseller scoping of the plan list depends on a page-scoped notion of
	// "current user" that a bearer token never populates, so the allowed-
	// plans restriction below never actually applies here -- every caller
	// sees every plan, admin or reseller alike.
	plans, err := paneldb.GetAllPlans(p.MySQL, nil)
	if err != nil || plans == nil {
		plans = []paneldb.RowMap{}
	}
	writeJSON(w, map[string]interface{}{"plans": plans})
}

func (p *APIPlans) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !isJSONRequest(r) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	var data map[string]interface{}
	json.NewDecoder(r.Body).Decode(&data)
	if data == nil {
		data = map[string]interface{}{}
	}

	actingUser := APIUserFromContext(r)
	if actingUser == nil {
		writeJSONError(w, http.StatusNotFound, "User not found")
		return
	}

	args := []string{
		"opencli", "plan-create",
		"name=" + jsonStringOr(data, "name", ""),
		"description=" + jsonStringOr(data, "description", ""),
		"emails=" + jsonStringOr(data, "email_limit", "0"),
		"max_email_quota=" + jsonStringOr(data, "max_email_quota", "0"),
		"max_hourly_email=" + jsonStringOr(data, "max_hourly_email", "0"),
		"ftp=" + jsonStringOr(data, "ftp_limit", "0"),
		"domains=" + jsonStringOr(data, "domains_limit", "0"),
		"websites=" + jsonStringOr(data, "websites_limit", "0"),
		"disk=" + jsonStringOr(data, "disk_limit", "0"),
		"inodes=" + jsonStringOr(data, "inodes_limit", "0"),
		"databases=" + jsonStringOr(data, "db_limit", "0"),
		"cpu=" + jsonStringOr(data, "cpu", "1"),
		"ram=" + jsonStringOr(data, "ram", "1"),
		"bandwidth=" + jsonStringOr(data, "bandwidth", "100"),
		"feature_set=" + jsonStringOr(data, "feature_set", "default"),
	}
	if actingUser.Role == "reseller" {
		args = append(args, "reseller="+actingUser.Username)
	}

	stdout, stderr, returncode := apiRunCapture(args...)
	if returncode == 0 {
		if planID, err := paneldb.GetPlanIDByName(p.MySQL, jsonStringOr(data, "name", "")); err == nil {
			_ = paneldb.SetPlanUpsell(p.MySQL, planID, jsonStringOr(data, "upsell_plan_id", ""), jsonStringOr(data, "upsell_url", ""))
		}
		msg := strings.TrimSpace(stdout)
		if msg == "" {
			msg = "Plan created successfully."
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": msg})
		return
	}
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = strings.TrimSpace(stdout)
	}
	writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
		"success": false,
		"error":   msg,
	})
}

// ServeDetail handles GET/PUT/PATCH/DELETE /api/plans/{plan_id}. Wrap with
// (*APIAuth).RequireAPIToken.
func (p *APIPlans) ServeDetail(w http.ResponseWriter, r *http.Request) {
	planIDStr := r.PathValue("plan_id")
	// The source route only matches a purely numeric segment; anything
	// else was never routed to this action at all, so it's reported as a
	// plain 404 rather than reaching the handler with a non-numeric ID.
	if _, err := strconv.Atoi(planIDStr); err != nil {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		p.handleDelete(w, r, planIDStr)
	case http.MethodPut, http.MethodPatch:
		p.handleEdit(w, r, planIDStr)
	default:
		p.handleGet(w, r, planIDStr)
	}
}

func (p *APIPlans) handleGet(w http.ResponseWriter, r *http.Request, planIDStr string) {
	plan, err := paneldb.GetPlanByID(p.MySQL, planIDStr)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Plan not found")
		return
	}
	writeJSON(w, map[string]interface{}{"plan": plan})
}

func (p *APIPlans) handleDelete(w http.ResponseWriter, r *http.Request, planIDStr string) {
	output, runErr := apiCheckOutputRun("opencli", "plan-delete", planIDStr, "--json")
	if runErr == nil {
		writeJSON(w, map[string]interface{}{
			"success": true,
			"message": "Plan deleted successfully.",
			"output":  output,
		})
		return
	}
	writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
		"success": false,
		"error":   output,
	})
}

func (p *APIPlans) handleEdit(w http.ResponseWriter, r *http.Request, planIDStr string) {
	if !isJSONRequest(r) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	var data map[string]interface{}
	json.NewDecoder(r.Body).Decode(&data)
	if data == nil {
		data = map[string]interface{}{}
	}

	diskLimit := jsonTruthyStringOr(data, "disk_limit", "0")

	args := []string{
		"opencli", "plan-edit", "id=" + planIDStr,
		"name=" + jsonStringOrNone(data, "name"),
		"description=" + jsonStringOrNone(data, "description"),
		"emails=" + jsonStringOr(data, "email_limit", "0"),
		"max_email_quota=" + jsonStringOr(data, "max_email_quota", "0"),
		"max_hourly_email=" + jsonStringOr(data, "max_hourly_email", "0"),
		"ftp=" + jsonStringOr(data, "ftp_limit", "0"),
		"domains=" + jsonStringOr(data, "domains_limit", "0"),
		"websites=" + jsonStringOr(data, "websites_limit", "0"),
		"disk=" + diskLimit,
		"inodes=" + jsonStringOr(data, "inodes_limit", "0"),
		"databases=" + jsonStringOr(data, "db_limit", "0"),
		"cpu=" + jsonStringOr(data, "cpu", "1"),
		"ram=" + jsonStringOr(data, "ram", "1"),
		"bandwidth=" + jsonStringOr(data, "bandwidth", "100"),
		"feature_set=" + jsonStringOr(data, "feature_set", "default"),
	}

	output, runErr := apiCheckOutputRun(args...)
	if runErr == nil {
		_, hasUpsellID := data["upsell_plan_id"]
		_, hasUpsellURL := data["upsell_url"]
		if hasUpsellID || hasUpsellURL {
			// A PATCH may send only one of the two upsell fields; the other
			// must be preserved rather than wiped by SetPlanUpsell (which
			// always writes both columns).
			current, _ := paneldb.GetPlanByID(p.MySQL, planIDStr)
			upsellID := jsonStringOr(data, "upsell_plan_id", jsonStringOr(current, "upsell_plan_id", ""))
			upsellURL := jsonStringOr(data, "upsell_url", jsonStringOr(current, "upsell_url", ""))
			_ = paneldb.SetPlanUpsell(p.MySQL, planIDStr, upsellID, upsellURL)
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": output})
		return
	}
	writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
		"success": false,
		"error":   output,
	})
}
