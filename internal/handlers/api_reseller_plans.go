package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"openadmin/internal/paneldb"
)

// ServeAPIResellerPlans handles GET/POST /api/plans/resellers, wrap with RequireAPIAdmin
func ServeAPIResellerPlans(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, listResellerPlans())
		return
	}

	var body struct {
		Action        string          `json:"action"`
		Name          string          `json:"name"`
		Description   string          `json:"description"`
		MaxAccounts   int             `json:"max_accounts"`
		MaxDiskBlocks int             `json:"max_disk_blocks"`
		AllowedPlans  json.RawMessage `json:"allowed_plans"`
	}
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing required fields.")
		return
	}

	var ok bool
	var message string
	switch body.Action {
	case "create", "update":
		plan := ResellerPlan{
			Name:          name,
			Description:   strings.TrimSpace(body.Description),
			MaxAccounts:   body.MaxAccounts,
			MaxDiskBlocks: body.MaxDiskBlocks,
			AllowedPlans:  apiPlanIDs(body.AllowedPlans),
		}
		ok, message = saveResellerPlan(plan, body.Action == "create")
	case "delete":
		ok, message = deleteResellerPlan(name)
	default:
		writeJSONError(w, http.StatusBadRequest, "Unknown action, use create, update or delete.")
		return
	}

	if ok {
		resp := map[string]interface{}{"success": true, "message": message}
		if p, exists := readResellerPlan(name); exists {
			resp["plan"] = p
		}
		writeJSON(w, resp)
		return
	}
	writeJSONFailure(w, http.StatusBadRequest, message)
}

// apiPlanIDs accepts [1,2], ["1","2"] or "1,2"
func apiPlanIDs(raw json.RawMessage) paneldb.PlanIDs {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return parsePlanIDList(s)
	}
	var ids paneldb.PlanIDs
	if json.Unmarshal(raw, &ids) == nil && ids != nil {
		return ids
	}
	return paneldb.PlanIDs{}
}
