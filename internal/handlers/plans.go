package handlers

import (
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/license"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// Plans bundles the /plans handlers.
type Plans struct {
	MySQL          *sql.DB
	Sessions       *auth.Manager
	LicenseChecker *license.Checker // nil on Community
}

// hasEnterpriseAccess reports whether the current request has an active
// Enterprise license, required to set a plan's upsell target.
func (p *Plans) hasEnterpriseAccess() bool {
	return p.LicenseChecker != nil && p.LicenseChecker.Valid()
}

var featureSetsDir = "/etc/openpanel/openpanel/features/"

func fetchFeatureSets(currentUser *admindb.User) []string {
	dir := featureSetsDir
	if currentUser.Role == "reseller" {
		dir = "/etc/openpanel/openpanel/features/" + currentUser.Username + "/"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var sets []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".txt") {
			sets = append(sets, strings.TrimSuffix(e.Name(), ".txt"))
		}
	}
	return sets
}

type plansListPageData struct {
	webtemplates.Chrome
	Plans         []paneldb.RowMap
	MySQLIsDown   bool
	SortCol       string
	SortDirection string
	HasEnterprise bool
	CSRFToken     string
	Flashes       []auth.Flash
}

// ServeList handles GET /plans.
func (p *Plans) ServeList(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)

	var allowed []int
	if currentUser.Role == "reseller" {
		ids, ok := paneldb.AllowedPlansForReseller(currentUser.Username)
		if !ok {
			p.renderList(w, r, nil, false)
			return
		}
		allowed = ids
	}

	plans, err := paneldb.GetAllPlansAndUserCount(p.MySQL, allowed)
	mysqlIsDown := err != nil
	if mysqlIsDown {
		plans = nil
	}

	if sortCol := r.URL.Query().Get("sort"); sortCol != "" {
		desc := strings.EqualFold(r.URL.Query().Get("direction"), "desc")
		sortRowMaps(plans, sortCol, desc)
	}

	p.renderList(w, r, plans, mysqlIsDown)
}

func (p *Plans) renderList(w http.ResponseWriter, r *http.Request, plans []paneldb.RowMap, mysqlIsDown bool) {
	annotateUpsellPlanNames(plans)
	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{"plans": plans})
		return
	}
	webtemplates.Render(w, "plans.html", plansListPageData{
		Chrome:        buildChrome(r, "Plans"),
		Plans:         plans,
		MySQLIsDown:   mysqlIsDown,
		SortCol:       r.URL.Query().Get("sort"),
		SortDirection: r.URL.Query().Get("direction"),
		HasEnterprise: p.hasEnterpriseAccess(),
		CSRFToken:     csrf.Token(r),
		Flashes:       auth.PopFlashes(w, r, p.Sessions),
	})
}

// annotateUpsellPlanNames sets each row's "upsell_plan_name" by resolving
// upsell_plan_id against the other plans in the same result set.
func annotateUpsellPlanNames(plans []paneldb.RowMap) {
	names := make(map[string]interface{}, len(plans))
	for _, plan := range plans {
		id := fmt.Sprintf("%v", plan["id"])
		names[id] = plan["name"]
	}
	for _, plan := range plans {
		upsellID := plan["upsell_plan_id"]
		if upsellID == nil {
			continue
		}
		id := fmt.Sprintf("%v", upsellID)
		plan["upsell_plan_name"] = names[id]
	}
}

func sortRowMaps(rows []paneldb.RowMap, col string, desc bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		vi, vj := rows[i][col], rows[j][col]
		if vi == nil {
			return !desc // nils first ascending, last descending
		}
		if vj == nil {
			return desc
		}
		less := compareAny(vi, vj)
		if desc {
			return !less && vi != vj
		}
		return less
	})
}

func compareAny(a, b interface{}) bool {
	switch av := a.(type) {
	case int64:
		if bv, ok := b.(int64); ok {
			return av < bv
		}
	case string:
		if bv, ok := b.(string); ok {
			return av < bv
		}
	}
	return false
}

type newPlanPageData struct {
	webtemplates.Chrome
	FormData      map[string]string
	FeatureSets   []string
	OtherPlans    []paneldb.RowMap
	HasEnterprise bool
	CSRFToken     string
	Flashes       []auth.Flash
}

// upsellCandidatePlans returns every plan the current user is allowed to
// see, for populating the "Upsell plan" dropdown -- optionally excluding
// excludeID (a plan can't upsell to itself).
func upsellCandidatePlans(db *sql.DB, currentUser *admindb.User, excludeID string) []paneldb.RowMap {
	if db == nil {
		return nil
	}
	var allowed []int
	if currentUser.Role == "reseller" {
		ids, ok := paneldb.AllowedPlansForReseller(currentUser.Username)
		if !ok {
			return nil
		}
		allowed = ids
	}
	plans, err := paneldb.GetAllPlans(db, allowed)
	if err != nil {
		return nil
	}
	if excludeID == "" {
		return plans
	}
	filtered := plans[:0]
	for _, plan := range plans {
		if sqlIDMatches(plan["id"], excludeID) {
			continue
		}
		filtered = append(filtered, plan)
	}
	return filtered
}

// sqlIDMatches compares a RowMap "id" value (driver-returned, usually
// int64 or []byte) against a string plan ID from a form/path value.
func sqlIDMatches(rowID interface{}, id string) bool {
	switch v := rowID.(type) {
	case int64:
		return strconv.FormatInt(v, 10) == id
	case []byte:
		return string(v) == id
	case string:
		return v == id
	default:
		return false
	}
}

// ServeNewForm handles GET /plans/new.
func (p *Plans) ServeNewForm(w http.ResponseWriter, r *http.Request) {
	webtemplates.Render(w, "plan_new.html", newPlanPageData{
		Chrome:        buildChrome(r, "New Plan"),
		FormData:      map[string]string{},
		FeatureSets:   fetchFeatureSets(auth.CurrentUser(r)),
		OtherPlans:    upsellCandidatePlans(p.MySQL, auth.CurrentUser(r), ""),
		HasEnterprise: p.hasEnterpriseAccess(),
		CSRFToken:     csrf.Token(r),
		Flashes:       auth.PopFlashes(w, r, p.Sessions),
	})
}

// HandleCreate handles POST /plans/new.
func (p *Plans) HandleCreate(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)

	fields := []string{"name", "description", "email_limit", "max_email_quota", "max_hourly_email",
		"ftp_limit", "domains_limit", "websites_limit", "disk_limit", "inodes_limit", "db_limit",
		"cpu", "ram", "bandwidth", "feature_set"}
	openCLIKeys := map[string]string{
		"email_limit": "emails", "ftp_limit": "ftp", "domains_limit": "domains",
		"websites_limit": "websites", "disk_limit": "disk", "inodes_limit": "inodes", "db_limit": "databases",
	}

	args := []string{"opencli", "plan-create"}
	for _, f := range fields {
		key := f
		if mapped, ok := openCLIKeys[f]; ok {
			key = mapped
		}
		args = append(args, key+"="+r.FormValue(f))
	}
	if currentUser.Role == "reseller" {
		args = append(args, "reseller="+currentUser.Username)
	}

	success, output := runOpenCLI("", args...)
	if success {
		if p.hasEnterpriseAccess() {
			if planID, err := paneldb.GetPlanIDByName(p.MySQL, r.FormValue("name")); err == nil {
				_ = paneldb.SetPlanUpsell(p.MySQL, planID, r.FormValue("upsell_plan_id"), r.FormValue("upsell_url"))
			}
		}
		if output == "" {
			output = "Plan created successfully."
		}
		auth.AddFlash(w, r, p.Sessions, output, "success")
	} else {
		auth.AddFlash(w, r, p.Sessions, output, "error")
	}
	http.Redirect(w, r, "/plans", http.StatusSeeOther)
}

// HandleDelete handles POST /plan/delete/{plan_name}.
func (p *Plans) HandleDelete(w http.ResponseWriter, r *http.Request) {
	planName := r.PathValue("plan_name")
	success, _ := runOpenCLI("", "opencli", "plan-delete", planName, "--json")
	if success {
		auth.AddFlash(w, r, p.Sessions, "Plan deleted successfully.", "success")
	} else {
		auth.AddFlash(w, r, p.Sessions, "Error deleting plan: "+planName, "error")
	}
	http.Redirect(w, r, "/plans", http.StatusSeeOther)
}

type editPlanPageData struct {
	webtemplates.Chrome
	PlanID        string
	Plan          paneldb.RowMap
	FeatureSets   []string
	OtherPlans    []paneldb.RowMap
	HasEnterprise bool
	CSRFToken     string
	Flashes       []auth.Flash
}

// ServeEdit handles GET/POST /plans/{plan_id}.
func (p *Plans) ServeEdit(w http.ResponseWriter, r *http.Request) {
	planID := r.PathValue("plan_id")

	if r.Method == http.MethodPost {
		p.handleEditPost(w, r, planID)
		return
	}

	plan, err := paneldb.GetPlanByID(p.MySQL, planID)
	if err != nil {
		plan = paneldb.RowMap{}
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{"plan": plan})
		return
	}

	webtemplates.Render(w, "plan_edit.html", editPlanPageData{
		Chrome:        buildChrome(r, "Edit Plan"),
		PlanID:        planID,
		Plan:          plan,
		FeatureSets:   fetchFeatureSets(auth.CurrentUser(r)),
		OtherPlans:    upsellCandidatePlans(p.MySQL, auth.CurrentUser(r), planID),
		HasEnterprise: p.hasEnterpriseAccess(),
		CSRFToken:     csrf.Token(r),
		Flashes:       auth.PopFlashes(w, r, p.Sessions),
	})
}

func (p *Plans) handleEditPost(w http.ResponseWriter, r *http.Request, planID string) {
	numericOrDefault := func(field, fallback string) string {
		if v := r.FormValue(field); v != "" {
			return v
		}
		return fallback
	}

	args := []string{
		"opencli", "plan-edit",
		"id=" + planID,
		"name=" + r.FormValue("name"),
		"description=" + r.FormValue("description"),
		"emails=" + numericOrDefault("email_limit", "0"),
		"max_email_quota=" + numericOrDefault("max_email_quota", "0"),
		"max_hourly_email=" + numericOrDefault("max_hourly_email", "0"),
		"ftp=" + numericOrDefault("ftp_limit", "0"),
		"domains=" + numericOrDefault("domains_limit", "0"),
		"websites=" + numericOrDefault("websites_limit", "0"),
		"disk=" + numericOrDefault("disk_limit", "0"),
		"inodes=" + numericOrDefault("inodes_limit", "0"),
		"databases=" + numericOrDefault("db_limit", "0"),
		"cpu=" + numericOrDefault("cpu", "1"),
		"ram=" + numericOrDefault("ram", "1"),
		"bandwidth=" + numericOrDefault("bandwidth", "100"),
		"feature_set=" + numericOrDefault("feature_set", "default"),
	}

	success, output := runOpenCLI("", args...)
	if success {
		if p.hasEnterpriseAccess() {
			_ = paneldb.SetPlanUpsell(p.MySQL, planID, r.FormValue("upsell_plan_id"), r.FormValue("upsell_url"))
		}
		auth.AddFlash(w, r, p.Sessions, output, "success")
	} else {
		auth.AddFlash(w, r, p.Sessions, output, "error")
	}
	http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
}

// ServeApplyLog handles GET /plan/apply/{filename}. filename is
// restricted to its base name (filepath.Base) before joining under /tmp,
// to prevent a path traversal vulnerability (e.g.
// filename="../../etc/passwd").
func (p *Plans) ServeApplyLog(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	safeName := filepath.Base(filename)
	if safeName != filename || safeName == "." || safeName == "/" {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	http.ServeFile(w, r, filepath.Join(os.TempDir(), safeName))
}

// isPublicIP reports whether ip is a publicly routable address.
// net.IP.IsPrivate() alone doesn't cover loopback/link-local addresses,
// so those are checked explicitly too.
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}

// planIPAddressesRun is injectable so tests never shell out to a real
// `hostname -I`.
var planIPAddressesRun = func() (string, error) {
	out, err := exec.Command("hostname", "-I").Output()
	return string(out), err
}

// ServeIPAddresses handles GET /system/ips/{username}. Access control:
// non-resellers are always allowed, a reseller must own the target
// account.
func (p *Plans) ServeIPAddresses(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	currentUser := auth.CurrentUser(r)
	if currentUser == nil || !paneldb.CheckIfOwnerForUser(p.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	out, err := planIPAddressesRun()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	publicIPs := []string{}
	for _, ip := range strings.Fields(out) {
		if parsed := net.ParseIP(ip); parsed != nil && isPublicIP(parsed) {
			publicIPs = append(publicIPs, ip)
		}
	}
	writeJSON(w, map[string]interface{}{"ip_addresses": publicIPs})
}
