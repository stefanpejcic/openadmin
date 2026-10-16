// Reseller packages are named limit templates for reseller accounts, stored as one JSON file each so opencli can apply them with `opencli admin update <user> --plan=<name>`.
// https://github.com/stefanpejcic/OpenPanel/discussions/1177
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// ResellerPlansDir is a var so tests can point it somewhere else
var ResellerPlansDir = "/etc/openpanel/openadmin/reseller_plans"

// same as opencli's reseller_plan_file check
var resellerPlanNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// opencli change_plan.sh uses this many blocks per GB
const diskBlocksPerGB = 1024000

type ResellerPlan struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	MaxAccounts   int             `json:"max_accounts"`
	MaxDiskBlocks int             `json:"max_disk_blocks"`
	AllowedPlans  paneldb.PlanIDs `json:"allowed_plans"`
	Resellers     []string        `json:"resellers,omitempty"`
}

func (p ResellerPlan) MaxDiskGB() string {
	if p.MaxDiskBlocks <= 0 {
		return "0"
	}
	return strconv.FormatFloat(float64(p.MaxDiskBlocks)/diskBlocksPerGB, 'f', -1, 64)
}

func resellerPlanPath(name string) string {
	return filepath.Join(ResellerPlansDir, name+".json")
}

func readResellerPlan(name string) (ResellerPlan, bool) {
	var p ResellerPlan
	if !resellerPlanNameRe.MatchString(name) {
		return p, false
	}
	raw, err := os.ReadFile(resellerPlanPath(name))
	if err != nil || json.Unmarshal(raw, &p) != nil {
		return p, false
	}
	p.Name = name
	if p.AllowedPlans == nil {
		p.AllowedPlans = paneldb.PlanIDs{}
	}
	return p, true
}

// listResellerPlans returns all plans sorted by name, each with the resellers currently on it
func listResellerPlans() []ResellerPlan {
	entries, _ := os.ReadDir(ResellerPlansDir)
	usage := resellersByPlan()
	plans := []ResellerPlan{}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		if p, ok := readResellerPlan(name); ok {
			p.Resellers = usage[name]
			plans = append(plans, p)
		}
	}
	sort.Slice(plans, func(i, j int) bool { return strings.ToLower(plans[i].Name) < strings.ToLower(plans[j].Name) })
	return plans
}

// resellersByPlan maps plan name to resellers whose limits file says they're on it
func resellersByPlan() map[string][]string {
	out := map[string][]string{}
	entries, _ := os.ReadDir(paneldb.ResellerConfigDir)
	for _, e := range entries {
		username, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if plan := readResellerData(username).ResellerPlan; plan != "" {
			out[plan] = append(out[plan], username)
		}
	}
	return out
}

func writeResellerPlan(p ResellerPlan) error {
	p.Resellers = nil
	if p.AllowedPlans == nil {
		p.AllowedPlans = paneldb.PlanIDs{}
	}
	out, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(ResellerPlansDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(resellerPlanPath(p.Name), out, 0644)
}

// parsePlanIDList turns "1,2,3" into ids, skipping junk
func parsePlanIDList(s string) paneldb.PlanIDs {
	ids := paneldb.PlanIDs{}
	for _, part := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && n > 0 {
			ids = append(ids, n)
		}
	}
	return ids
}

func validateResellerPlan(p ResellerPlan) string {
	if !resellerPlanNameRe.MatchString(p.Name) {
		return "Plan name can only contain letters, numbers, - and _ (max 64 characters)."
	}
	if p.MaxAccounts < 0 || p.MaxDiskBlocks < 0 {
		return "Limits can't be negative."
	}
	if len(p.Description) > 255 {
		return "Description is too long (max 255 characters)."
	}
	return ""
}

// saveResellerPlan creates or updates a plan, on update it reapplies it to every reseller on it and returns the ones that failed
func saveResellerPlan(p ResellerPlan, create bool) (ok bool, message string) {
	if msg := validateResellerPlan(p); msg != "" {
		return false, msg
	}
	_, exists := readResellerPlan(p.Name)
	if create && exists {
		return false, "Reseller package '" + p.Name + "' already exists."
	}
	if !create && !exists {
		return false, "Reseller package '" + p.Name + "' does not exist."
	}
	if err := writeResellerPlan(p); err != nil {
		return false, "Failed to save reseller package: " + err.Error()
	}
	if create {
		return true, "Reseller package '" + p.Name + "' created."
	}

	var failed []string
	for _, reseller := range resellersByPlan()[p.Name] {
		if ok, out := runOpenCLI(adminCommandError, "opencli", "admin", "update", reseller, "--plan="+p.Name); !ok {
			failed = append(failed, reseller+" ("+out+")")
		}
	}
	if len(failed) > 0 {
		return true, "Reseller package '" + p.Name + "' saved, but it couldn't be applied to: " + strings.Join(failed, ", ")
	}
	return true, "Reseller package '" + p.Name + "' saved."
}

func deleteResellerPlan(name string) (ok bool, message string) {
	if _, exists := readResellerPlan(name); !exists {
		return false, "Reseller package '" + name + "' does not exist."
	}
	if users := resellersByPlan()[name]; len(users) > 0 {
		return false, fmt.Sprintf("Reseller package '%s' is used by %d reseller(s): %s. Move them to another package first.", name, len(users), strings.Join(users, ", "))
	}
	if err := os.Remove(resellerPlanPath(name)); err != nil {
		return false, "Failed to delete reseller package: " + err.Error()
	}
	return true, "Reseller package '" + name + "' deleted."
}

// applyResellerPlan moves a reseller to a plan, opencli refuses downgrades below current usage
func applyResellerPlan(username, plan string) (bool, string) {
	if _, ok := readResellerPlan(plan); !ok {
		return false, "Reseller package '" + plan + "' does not exist."
	}
	ok, out := runOpenCLI(adminCommandError, "opencli", "admin", "update", username, "--plan="+plan)
	if ok {
		return true, "Reseller " + username + " moved to package '" + plan + "'."
	}
	return false, opencliResultMessage(ok, out)
}

// ResellerPlans bundles the /plans/resellers pages, admin only
type ResellerPlans struct {
	Resellers *Resellers
}

// ServeList handles GET/POST /plans/resellers
func (rp *ResellerPlans) ServeList(w http.ResponseWriter, r *http.Request) {
	sessions := rp.Resellers.Sessions
	if r.Method == http.MethodPost {
		r.ParseForm()
		action := r.PostFormValue("action")
		name := strings.TrimSpace(r.PostFormValue("name"))
		var ok bool
		var msg string
		switch action {
		case "create", "update":
			ok, msg = saveResellerPlan(resellerPlanFromForm(r, name), action == "create")
		case "delete":
			ok, msg = deleteResellerPlan(name)
		default:
			msg = "Unknown action."
		}
		if ok {
			auth.AddFlash(w, r, sessions, msg, "success")
		} else {
			auth.AddFlash(w, r, sessions, "Error: "+msg, "error")
		}
		if !ok && action == "update" {
			http.Redirect(w, r, "/plans/resellers/"+name, http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/plans/resellers", http.StatusSeeOther)
		return
	}

	plans := listResellerPlans()
	sortCol, sortDirection := readSort(r, resellerPlanSortKeys)
	sortResellerPlans(plans, sortCol, sortDirection)

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, plans)
		return
	}
	webtemplates.Render(w, "reseller_plans.html", mergeChrome(map[string]interface{}{
		"ResellerPlans": plans,
		"SortCol":       sortCol,
		"SortDirection": sortDirection,
		"Plans":         rp.userPlans(),
		"Flashes":       auth.PopFlashes(w, r, sessions),
	}, r, "Reseller Packages"))
}

// ServeEdit handles GET /plans/resellers/{name}
func (rp *ResellerPlans) ServeEdit(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	plan, ok := readResellerPlan(name)
	if !ok {
		auth.AddFlash(w, r, rp.Resellers.Sessions, "Error: Reseller package '"+name+"' does not exist.", "error")
		http.Redirect(w, r, "/plans/resellers", http.StatusSeeOther)
		return
	}
	plan.Resellers = resellersByPlan()[name]
	webtemplates.Render(w, "reseller_plan_edit.html", mergeChrome(map[string]interface{}{
		"Plan":    plan,
		"Plans":   rp.userPlans(),
		"Flashes": auth.PopFlashes(w, r, rp.Resellers.Sessions),
	}, r, "Edit Reseller Package: "+name))
}

func (rp *ResellerPlans) userPlans() []paneldb.RowMap {
	if rp.Resellers.MySQL == nil {
		return nil
	}
	plans, _ := paneldb.GetAllPlans(rp.Resellers.MySQL, nil)
	return plans
}

func resellerPlanFromForm(r *http.Request, name string) ResellerPlan {
	maxAccounts, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("max_accounts")))
	diskGB, _ := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue("max_disk_gb")), 64)
	return ResellerPlan{
		Name:          name,
		Description:   strings.TrimSpace(r.PostFormValue("description")),
		MaxAccounts:   maxAccounts,
		MaxDiskBlocks: int(diskGB * diskBlocksPerGB),
		AllowedPlans:  parsePlanIDList(r.PostFormValue("allowed_plans")),
	}
}

var resellerPlanSortKeys = map[string]bool{"name": true, "max_accounts": true, "max_disk": true, "user_packages": true, "resellers": true}

// unlimitedLast makes 0 (unlimited) sort above every real limit
func unlimitedLast(n int) int {
	if n <= 0 {
		return int(^uint(0) >> 1)
	}
	return n
}

func sortResellerPlans(plans []ResellerPlan, col, direction string) {
	var less func(a, b ResellerPlan) bool
	switch col {
	case "name":
		less = func(a, b ResellerPlan) bool { return strings.ToLower(a.Name) < strings.ToLower(b.Name) }
	case "max_accounts":
		less = func(a, b ResellerPlan) bool { return unlimitedLast(a.MaxAccounts) < unlimitedLast(b.MaxAccounts) }
	case "max_disk":
		less = func(a, b ResellerPlan) bool { return unlimitedLast(a.MaxDiskBlocks) < unlimitedLast(b.MaxDiskBlocks) }
	case "user_packages":
		less = func(a, b ResellerPlan) bool { return len(a.AllowedPlans) < len(b.AllowedPlans) }
	case "resellers":
		less = func(a, b ResellerPlan) bool { return len(a.Resellers) < len(b.Resellers) }
	default:
		return
	}
	sortBy(plans, direction, less)
}
