package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// BulkRoutes registers every page's POST .../bulk endpoint, each one replays the selected rows through the page's own single-row routes on mux
func BulkRoutes(mux *http.ServeMux, sessions *auth.Manager, opts auth.Options, users *Users, domains *Domains) {
	mux.HandleFunc("POST /users/bulk", auth.RequireLogin(sessions, opts, func(w http.ResponseWriter, r *http.Request) {
		ServeBulkDispatch(sessions, mux, w, r, users.bulkActions(r), usersBulkRoute)
	}))
	admin := func(pattern string, actions func() []webtemplates.BulkAction, route BulkRoute) {
		mux.HandleFunc(pattern, auth.RequireAdmin(sessions, opts, func(w http.ResponseWriter, r *http.Request) {
			ServeBulkDispatch(sessions, mux, w, r, actions(), route)
		}))
	}
	mux.HandleFunc("POST /domains/bulk", auth.RequireAdmin(sessions, opts, func(w http.ResponseWriter, r *http.Request) {
		ServeBulkDispatch(sessions, mux, w, r, DomainsBulkActions(domains.ownerPHPVersions(nil)), domainsBulkRoute)
	}))
	admin("POST /services/bulk", ServicesBulkActions, servicesBulkRoute)
	admin("POST /server/processes/bulk", ProcessesBulkActions, processesBulkRoute)
	admin("POST /tasks/bulk", ProcessesBulkActions, processesBulkRoute)
	admin("POST /administrators/bulk", func() []webtemplates.BulkAction { return AccountsBulkActions("administrators") }, accountsBulkRoute("/administrators"))
	mux.HandleFunc("POST /notifications/bulk", auth.RequireAdmin(sessions, opts, func(w http.ResponseWriter, r *http.Request) {
		serveBulkDispatchOrdered(sessions, mux, w, r, NotificationsBulkActions(), notificationsBulkRoute, notificationsBulkOrder)
	}))
	admin("POST /backups/system/bulk", SystemBackupsBulkActions, systemBackupsBulkRoute)
	admin("POST /security/waf/rules/bulk", WAFRulesBulkActions, wafRulesBulkRoute)
	admin("POST /settings/locales/bulk", LocalesBulkActions, localesBulkRoute)
	mux.HandleFunc("POST /plans/bulk", auth.RequireLogin(sessions, opts, func(w http.ResponseWriter, r *http.Request) {
		// the plans this admin or reseller sees, read once for the whole run
		var list struct {
			Plans []paneldb.RowMap `json:"plans"`
		}
		loaded := false
		plans := func() []paneldb.RowMap {
			if !loaded {
				loaded = true
				_ = bulkGetJSON(mux, r, "/plans?output=json", &list)
			}
			return list.Plans
		}
		ServeBulkDispatch(sessions, mux, w, r, PlansBulkActions(fetchFeatureSets(auth.CurrentUser(r))), plansBulkRoute(plans))
	}))
	// the replayed /containers routes check the user is owned by whoever is logged in
	mux.HandleFunc("POST /containers/{username}/bulk", auth.RequireLogin(sessions, opts, func(w http.ResponseWriter, r *http.Request) {
		ServeBulkDispatch(sessions, mux, w, r, UserContainersBulkActions(), userContainersBulkRoute(r.PathValue("username")))
	}))
	mux.HandleFunc("POST /server/crons/bulk", auth.RequireAdmin(sessions, opts, func(w http.ResponseWriter, r *http.Request) {
		// the jobs as they are now, read once, a job keeps its schedule or logging while the other one changes
		var jobs []CronJob
		loaded := false
		current := func() []CronJob {
			if !loaded {
				loaded = true
				_ = bulkGetJSON(mux, r, "/server/crons?output=json", &jobs)
			}
			return jobs
		}
		ServeBulkDispatch(sessions, mux, w, r, CronsBulkActions(), cronsBulkRoute(current))
	}))
	mux.HandleFunc("POST /emails/bulk", auth.RequireLogin(sessions, opts, func(w http.ResponseWriter, r *http.Request) {
		ServeBulkDispatch(sessions, mux, w, r, EmailAccountsBulkActions(), emailAccountsBulkRoute)
	}))
	mux.HandleFunc("POST /resellers/bulk", auth.RequireLogin(sessions, opts, func(w http.ResponseWriter, r *http.Request) {
		ServeBulkDispatch(sessions, mux, w, r, AccountsBulkActions("resellers"), accountsBulkRoute("/resellers"))
	}))
}

// UsersBulkActions offers plans this admin or reseller may assign and the server's public IPs
func UsersBulkActions(plans []paneldb.RowMap, ips []string) []webtemplates.BulkAction {
	planOpts := make([]webtemplates.BulkOption, 0, len(plans))
	for _, p := range plans {
		name, _ := p["name"].(string)
		planOpts = append(planOpts, webtemplates.BulkOption{Value: fmt.Sprint(p["id"]), Label: name})
	}
	// "delete" is opencli user-ip's way back to the shared IP, the shared IP itself can't be a dedicated one
	ipOpts := []webtemplates.BulkOption{{Value: "delete", Label: strings.TrimSpace("Shared IP " + chromeSite.PublicIP)}}
	for _, ip := range ips {
		if ip != chromeSite.PublicIP {
			ipOpts = append(ipOpts, webtemplates.BulkOption{Value: ip, Label: ip + " (dedicated)"})
		}
	}
	return []webtemplates.BulkAction{
		{Key: "suspend", Label: "Suspend", Confirm: "Suspend the selected users? Their websites stop working until they are unsuspended."},
		{Key: "unsuspend", Label: "Unsuspend", Confirm: "Unsuspend the selected users?"},
		{Key: "plan", Label: "Change plan", Confirm: "Move the selected users to this plan:",
			Input: &webtemplates.BulkInput{Type: "select", Options: planOpts}},
		{Key: "password", Label: "Change password", Confirm: "Set this password for the selected users:",
			Input: &webtemplates.BulkInput{Type: "password", Placeholder: "New password", Pattern: ".{8,}", Hint: "at least 8 characters"}},
		{Key: "email", Label: "Change email", Confirm: "Set this email address for the selected users:",
			Input: &webtemplates.BulkInput{Type: "email", Placeholder: "user@example.com"}},
		{Key: "ip", Label: "Change IP", Confirm: "Move the selected users to this IP address, their domains follow:",
			Input: &webtemplates.BulkInput{Type: "select", Options: ipOpts}},
		{Key: "twofa", Label: "Disable 2FA", Confirm: "Turn off two-factor authentication for the selected users? They can log in with just their password until they set it up again."},
		{Key: "backup", Label: "Generate backup", Confirm: "Start a full account backup for each selected user? They run in the background, one per user."},
		{Key: "delete", Label: "Delete", Confirm: "Permanently delete the selected users with all their websites, databases, emails and files? This cannot be undone.", Danger: true},
	}
}

// bulkActions lists the plans the admin or reseller behind r may assign, same as the /users page
func (u *Users) bulkActions(r *http.Request) []webtemplates.BulkAction {
	var allowed []int
	if owner := u.resellerScope(r); owner != "" {
		if ids, ok := paneldb.AllowedPlansForReseller(owner); ok {
			allowed = ids
		}
	}
	plans, _ := paneldb.GetAllPlans(u.MySQL, allowed)
	return UsersBulkActions(plans, serverPublicIPs())
}

// usersEditFields are the /user/edit form fields the one-value actions fill in
var usersEditFields = map[string]string{"password": "new_password", "email": "new_email", "ip": "new_ip", "plan": "plan_id"}

// usersBulkRoute items are plain usernames, without the SUSPENDED_ prefix
func usersBulkRoute(action, value, username string) (*BulkCall, BulkResult) {
	if username == "" || strings.ContainsAny(username, "/?#") {
		return BulkSkip("Invalid username.")
	}
	path := "/user/" + action + "/" + url.PathEscape(username)
	switch action {
	case "suspend", "unsuspend":
		return BulkDo(BulkCall{Method: http.MethodPost, Path: path})
	case "delete":
		// the same confirmation the Delete dialog on the user page asks for
		return BulkDo(BulkCall{Method: http.MethodPost, Path: path, Form: url.Values{"confirmation": {username}}})
	case "password", "email", "ip", "plan":
		// the Edit tab's form with only this one field filled in
		return BulkDo(BulkCall{Method: http.MethodPost, Path: "/user/edit/" + url.PathEscape(username), Form: url.Values{usersEditFields[action]: {value}}})
	case "backup":
		return BulkDo(BulkCall{Method: http.MethodPost, Path: "/user/export/create/" + url.PathEscape(username)})
	case "twofa":
		return BulkDo(BulkCall{Method: http.MethodPost, Path: "/users/" + url.PathEscape(username) + "/account-setting/twofa"})
	}
	return BulkSkip("Unknown bulk action.")
}

// DomainsBulkActions offers the PHP versions installed for any of the domain owners
func DomainsBulkActions(phpVersions []string) []webtemplates.BulkAction {
	phpOpts := make([]webtemplates.BulkOption, 0, len(phpVersions))
	for _, v := range phpVersions {
		phpOpts = append(phpOpts, webtemplates.BulkOption{Value: v, Label: "PHP " + v})
	}
	return []webtemplates.BulkAction{
		{Key: "php", Label: "Change PHP version", Confirm: "Set the PHP version for the selected domains:",
			Input: &webtemplates.BulkInput{Type: "select", Options: phpOpts}},
		{Key: "hsts_on", Label: "Enable HSTS", Confirm: "Enable HSTS for the selected domains?"},
		{Key: "hsts_off", Label: "Disable HSTS", Confirm: "Disable HSTS for the selected domains?"},
		{Key: "waf_on", Label: "Enable WAF", Confirm: "Enable the WAF for the selected domains?"},
		{Key: "waf_off", Label: "Disable WAF", Confirm: "Disable the WAF for the selected domains?"},
		{Key: "suspend", Label: "Suspend", Confirm: "Suspend the selected domains?"},
		{Key: "unsuspend", Label: "Unsuspend", Confirm: "Unsuspend the selected domains?"},
		{Key: "delete", Label: "Delete", Confirm: "Permanently delete the selected domains? Their files are kept, but DNS zones, SSL and web server config are removed.", Danger: true},
	}
}

// ownerPHPVersions merges the installed PHP versions of these domain rows' owners, newest first, nil rows means every domain
func (d *Domains) ownerPHPVersions(rows []paneldb.RowMap) []string {
	if rows == nil {
		var err error
		if rows, err = paneldb.GetAllDomains(d.MySQL); err != nil {
			return nil
		}
	}
	seenOwner, seen := map[string]bool{}, map[string]bool{}
	var versions []string
	for _, row := range rows {
		owner, _ := row["username"].(string)
		owner = stripSuspendedPrefix(owner)
		if owner == "" || seenOwner[owner] {
			continue
		}
		seenOwner[owner] = true
		context, err := queryContextByUsername(d.MySQL, owner)
		if err != nil || context == "" {
			continue
		}
		list, _ := cachedAvailablePHPVersions(context)
		for _, v := range list {
			if !seen[v] {
				seen[v] = true
				versions = append(versions, v)
			}
		}
	}
	sort.Slice(versions, func(i, j int) bool { return phpVersionLess(versions[j], versions[i]) })
	return versions
}

// phpVersionLess compares "8.10" after "8.9"
func phpVersionLess(a, b string) bool {
	am, an, _ := strings.Cut(a, ".")
	bm, bn, _ := strings.Cut(b, ".")
	ai, _ := strconv.Atoi(am)
	bi, _ := strconv.Atoi(bm)
	if ai != bi {
		return ai < bi
	}
	ci, _ := strconv.Atoi(an)
	di, _ := strconv.Atoi(bn)
	return ci < di
}

var bulkPHPVersionRE = regexp.MustCompile(`^\d+\.\d+$`)

func domainsBulkRoute(action, value, domain string) (*BulkCall, BulkResult) {
	if !isDomain(domain) {
		return BulkSkip("Invalid domain.")
	}
	toggle := func(feature string, form url.Values) (*BulkCall, BulkResult) {
		form.Set("domain_name", domain)
		return BulkDo(BulkCall{Method: http.MethodPost, Path: "/domains/" + feature + "/toggle", Form: form})
	}
	switch action {
	case "php":
		if !bulkPHPVersionRE.MatchString(value) {
			return BulkSkip("Invalid PHP version.")
		}
		return BulkDo(BulkCall{Method: http.MethodPost, Path: "/php/" + domain, Form: url.Values{"version": {value}}})
	case "hsts_on":
		return toggle("hsts", url.Values{"hsts_action": {"On"}})
	case "hsts_off":
		return toggle("hsts", url.Values{"hsts_action": {"Off"}})
	case "waf_on":
		return toggle("waf", url.Values{"modsec_action": {"On"}})
	case "waf_off":
		return toggle("waf", url.Values{"modsec_action": {"Off"}})
	case "suspend", "unsuspend", "delete":
		return toggle(action, url.Values{})
	}
	return BulkSkip("Unknown bulk action.")
}

func ServicesBulkActions() []webtemplates.BulkAction {
	return []webtemplates.BulkAction{
		{Key: "start", Label: "Start", Confirm: "Start the selected services?"},
		{Key: "stop", Label: "Stop", Confirm: "Stop the selected services? Websites or features that depend on them stop working."},
		{Key: "restart", Label: "Restart", Confirm: "Restart the selected services?"},
	}
}

// servicesBulkRoute items are type:real_name, the same pair the row's own buttons post
func servicesBulkRoute(action, _, item string) (*BulkCall, BulkResult) {
	container, realName, ok := strings.Cut(item, ":")
	if !ok || realName == "" {
		return BulkSkip("Invalid service.")
	}
	form := url.Values{"real_name": {realName}, "container": {container}, "action": {action}}
	return BulkDo(BulkCall{Label: realName, Method: http.MethodPost, Path: "/services", Form: form})
}

func ProcessesBulkActions() []webtemplates.BulkAction {
	return []webtemplates.BulkAction{
		{Key: "kill", Label: "Kill", Confirm: "Kill the selected processes?", Danger: true},
	}
}

func processesBulkRoute(_, _, pid string) (*BulkCall, BulkResult) {
	if n, err := strconv.Atoi(pid); err != nil || n <= 1 {
		return BulkSkip("Invalid PID.")
	}
	return BulkDo(BulkCall{Method: http.MethodPost, Path: "/server/processes/" + pid + "/kill"})
}

func AccountsBulkActions(kind string) []webtemplates.BulkAction {
	return []webtemplates.BulkAction{
		{Key: "suspend", Label: "Suspend", Confirm: "Suspend the selected " + kind + "? They can't log in until unsuspended."},
		{Key: "unsuspend", Label: "Unsuspend", Confirm: "Unsuspend the selected " + kind + "?"},
		{Key: "delete", Label: "Delete", Confirm: "Permanently delete the selected " + kind + "? This cannot be undone.", Danger: true},
	}
}

// accountsBulkRoute posts the same action/username form as the row menus on /resellers and /administrators
func accountsBulkRoute(path string) BulkRoute {
	return func(action, _, username string) (*BulkCall, BulkResult) {
		if !adminUsernameRe.MatchString(username) {
			return BulkSkip("Invalid username.")
		}
		return BulkDo(BulkCall{Method: http.MethodPost, Path: path, Form: url.Values{"action": {action}, "username": {username}}})
	}
}

// PlansBulkActions: Update picks one limit of the edit form and sets it on every selected plan
// cronNeverSchedule is Feb 31st, the same "never fires" schedule System Backups uses for disabled
const cronNeverSchedule = "59 23 31 2 *"

func CronsBulkActions() []webtemplates.BulkAction {
	return []webtemplates.BulkAction{
		{Key: "schedule", Label: "Change schedule", Confirm: "Set this schedule for the selected cron jobs:",
			Input: &webtemplates.BulkInput{Type: "text", Pattern: `[0-9*/,\-]+( [0-9*/,\-]+){4}`, Placeholder: "0 3 * * *", Hint: "minute hour day month weekday"}},
		{Key: "disable", Label: "Disable", Confirm: "Disable the selected cron jobs? Their schedule is set to Feb 31st (" + cronNeverSchedule + "), so they never run until you set a new one."},
		{Key: "log_on", Label: "Logging on", Confirm: "Log each run of the selected cron jobs to /var/log/openpanel-cron.log?"},
		{Key: "log_off", Label: "Logging off", Confirm: "Stop logging the runs of the selected cron jobs?"},
	}
}

// cronsBulkRoute items are line numbers in /etc/cron.d/openpanel, it posts the page's own form with only that job's fields
func cronsBulkRoute(jobs func() []CronJob) BulkRoute {
	return func(action, value, line string) (*BulkCall, BulkResult) {
		for _, j := range jobs() {
			if strconv.Itoa(j.LineNumber) != line {
				continue
			}
			schedule, logging := j.Schedule, j.Log
			switch action {
			case "schedule":
				schedule = value
			case "disable":
				schedule = cronNeverSchedule
			case "log_on", "log_off":
				logging = action == "log_on"
			}
			form := url.Values{}
			for i, part := range strings.Fields(schedule) {
				form.Set(line+"_schedule_"+strconv.Itoa(i), part)
			}
			if logging {
				form.Set(line+"_logging", "on")
			}
			return BulkDo(BulkCall{Label: j.Command, Method: http.MethodPost, Path: "/server/crons", Form: form})
		}
		return BulkSkip("Cron job not found, reload the page.")
	}
}

func EmailAccountsBulkActions() []webtemplates.BulkAction {
	return []webtemplates.BulkAction{
		{Key: "password", Label: "Change password", Confirm: "Set this password for the selected email accounts:",
			Input: &webtemplates.BulkInput{Type: "password", Placeholder: "New password"}},
		{Key: "quota", Label: "Set quota", Confirm: "Set the mailbox quota for the selected email accounts:",
			Input: &webtemplates.BulkInput{Type: "text", Pattern: "[0-9]+[KMGT]", Placeholder: "2G", Hint: "a number with K, M, G or T, e.g. 512M or 2G"}},
		{Key: "quota_del", Label: "Remove quota", Confirm: "Remove the mailbox quota limit from the selected email accounts?"},
		{Key: "restrict_send", Label: "Restrict sending", Confirm: "Block the selected email accounts from sending email?"},
		{Key: "restrict_receive", Label: "Restrict receiving", Confirm: "Block the selected email accounts from receiving email?"},
		{Key: "allow_send", Label: "Allow sending", Confirm: "Remove the sending restriction from the selected email accounts?"},
		{Key: "allow_receive", Label: "Allow receiving", Confirm: "Remove the receiving restriction from the selected email accounts?"},
		{Key: "delete", Label: "Delete", Confirm: "Permanently delete the selected email accounts and their mail? This cannot be undone.", Danger: true},
	}
}

// emailAccountsBulkRoute replays the JSON calls the row menu on /emails/accounts makes
func emailAccountsBulkRoute(action, value, email string) (*BulkCall, BulkResult) {
	if !strings.Contains(email, "@") || strings.ContainsAny(email, " /") {
		return BulkSkip("Invalid email address.")
	}
	call := func(path string, body map[string]any) (*BulkCall, BulkResult) {
		return BulkDo(BulkCall{Method: http.MethodPost, Path: path, JSON: body})
	}
	restrict := map[string][2]string{"restrict_send": {"add", "send"}, "restrict_receive": {"add", "receive"}, "allow_send": {"del", "send"}, "allow_receive": {"del", "receive"}}
	switch action {
	case "password":
		return call("/emails/api/update-password", map[string]any{"email": email, "password": value})
	case "quota":
		return call("/emails/api/quota-set", map[string]any{"email": email, "quota": value})
	case "quota_del":
		return call("/emails/api/quota-del", map[string]any{"email": email})
	case "restrict_send", "restrict_receive", "allow_send", "allow_receive":
		return call("/emails/api/restrict", map[string]any{"email": email, "action": restrict[action][0], "type": restrict[action][1]})
	case "delete":
		return call("/emails/api/delete", map[string]any{"emails": []string{email}})
	}
	return BulkSkip("Unknown bulk action.")
}

func PlansBulkActions(featureSets []string) []webtemplates.BulkAction {
	count := func(key, label string) webtemplates.BulkField {
		return webtemplates.BulkField{Key: key, Label: label, Input: webtemplates.BulkInput{Type: "number", Min: "0", Step: "1", Hint: "0 = unlimited"}}
	}
	amount := func(key, label, unit string) webtemplates.BulkField {
		return webtemplates.BulkField{Key: key, Label: label, Input: webtemplates.BulkInput{Type: "number", Min: "0", Step: "1", Placeholder: unit}}
	}
	fsOpts := make([]webtemplates.BulkOption, 0, len(featureSets))
	for _, f := range featureSets {
		fsOpts = append(fsOpts, webtemplates.BulkOption{Value: f, Label: f})
	}
	fields := []webtemplates.BulkField{
		amount("disk_limit", "Disk (GB)", "GB"),
		amount("inodes_limit", "Inodes", "inodes"),
		amount("cpu", "CPU (cores)", "cores"),
		amount("ram", "Memory (GB)", "GB"),
		amount("bandwidth", "Port speed (Mbit/s)", "Mbit/s"),
		count("domains_limit", "Domains"),
		count("websites_limit", "Websites"),
		count("db_limit", "Databases"),
		count("email_limit", "Email accounts"),
		{Key: "max_email_quota", Label: "Max email quota", Input: webtemplates.BulkInput{Type: "text", Pattern: "[0-9]+[KMGT]?", Placeholder: "1G", Hint: "a number with K, M, G or T, e.g. 1G"}},
		amount("max_hourly_email", "Max emails per hour", "emails"),
		count("ftp_limit", "FTP accounts"),
	}
	if len(fsOpts) > 0 {
		fields = append(fields, webtemplates.BulkField{Key: "feature_set", Label: "Feature set", Input: webtemplates.BulkInput{Type: "select", Options: fsOpts}})
	}
	return []webtemplates.BulkAction{
		{Key: "update", Label: "Update", Confirm: "Set this limit on the selected plans, users on them get it too:", Input: &webtemplates.BulkInput{Fields: fields}},
		{Key: "delete", Label: "Delete", Confirm: "Delete the selected plans? Plans that still have users can't be deleted.", Danger: true},
	}
}

// plansBulkRoute items are plan names, Update posts the plan's whole edit form with the one field changed
func plansBulkRoute(plans func() []paneldb.RowMap) BulkRoute {
	return func(action, value, name string) (*BulkCall, BulkResult) {
		if name == "" || strings.ContainsAny(name, "/?#") {
			return BulkSkip("Invalid plan name.")
		}
		if action == "delete" {
			return BulkDo(BulkCall{Method: http.MethodPost, Path: "/plan/delete/" + url.PathEscape(name)})
		}
		field, val, _ := strings.Cut(value, "=")
		for _, p := range plans() {
			if p["name"] != name {
				continue
			}
			form := planEditForm(p)
			form.Set(field, val)
			return BulkDo(BulkCall{Label: name, Method: http.MethodPost, Path: "/plans/" + fmt.Sprint(p["id"]), Form: form})
		}
		return BulkSkip("Plan not found.")
	}
}

// planEditForm is what the Edit plan page would post for p unchanged
func planEditForm(p paneldb.RowMap) url.Values {
	str := func(k string) string {
		if p[k] == nil {
			return ""
		}
		return fmt.Sprint(p[k])
	}
	form := url.Values{}
	for _, k := range []string{"name", "description", "inodes_limit", "cpu", "bandwidth", "domains_limit", "websites_limit", "db_limit", "email_limit", "max_email_quota", "max_hourly_email", "ftp_limit", "feature_set", "upsell_plan_id", "upsell_url"} {
		form.Set(k, str(k))
	}
	form.Set("disk_limit", strings.TrimSuffix(str("disk_limit"), " GB"))
	form.Set("ram", strings.TrimSuffix(str("ram"), "g"))
	return form
}

func NotificationsBulkActions() []webtemplates.BulkAction {
	return []webtemplates.BulkAction{
		{Key: "mark_as_read", Label: "Mark as read", Confirm: "Mark the selected notifications as read?"},
		{Key: "delete", Label: "Delete", Confirm: "Delete the selected notifications?", Danger: true},
	}
}

// notificationsBulkRoute items are the row's line number, counted from the newest entry
func notificationsBulkRoute(action, _, line string) (*BulkCall, BulkResult) {
	if n, err := strconv.Atoi(line); err != nil || n < 1 {
		return BulkSkip("Invalid notification.")
	}
	return BulkDo(BulkCall{Label: "#" + line, Method: http.MethodPost, Path: "/notifications/" + action + "/" + line})
}

// notificationsBulkOrder deletes the oldest first, since deleting a line renumbers every older one after it
func notificationsBulkOrder(action string, items []string) {
	if action != "delete" {
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, _ := strconv.Atoi(items[i])
		b, _ := strconv.Atoi(items[j])
		return a > b
	})
}

func SystemBackupsBulkActions() []webtemplates.BulkAction {
	return []webtemplates.BulkAction{
		{Key: "delete", Label: "Delete", Confirm: "Permanently delete the selected backups? This cannot be undone.", Danger: true},
	}
}

func systemBackupsBulkRoute(_, _, name string) (*BulkCall, BulkResult) {
	if name == "" || name != filepath.Base(name) {
		return BulkSkip("Invalid backup name.")
	}
	return BulkDo(BulkCall{Method: http.MethodPost, Path: "/backups/system/delete/" + url.PathEscape(name)})
}

func WAFRulesBulkActions() []webtemplates.BulkAction {
	return []webtemplates.BulkAction{
		{Key: "on", Label: "Enable", Confirm: "Enable the selected rule sets? Restart Caddy afterwards to apply."},
		{Key: "off", Label: "Disable", Confirm: "Disable the selected rule sets? Restart Caddy afterwards to apply."},
	}
}

func wafRulesBulkRoute(action, _, name string) (*BulkCall, BulkResult) {
	return BulkDo(BulkCall{Method: http.MethodPost, Path: "/security/waf/rules", Form: url.Values{"rule_name": {name}, "action": {action}}})
}

func LocalesBulkActions() []webtemplates.BulkAction {
	return []webtemplates.BulkAction{
		{Key: "locale", Label: "Install", Confirm: "Install the selected locales?"},
		{Key: "update", Label: "Update", Confirm: "Update the selected locales to the latest translations?"},
		{Key: "delete", Label: "Delete", Confirm: "Delete the selected locales? Users who picked them fall back to the default locale.", Danger: true},
	}
}

// localesBulkRoute posts the same field the row's own install/update/delete buttons do, the action key is that field's name
func localesBulkRoute(action, _, locale string) (*BulkCall, BulkResult) {
	return BulkDo(BulkCall{Method: http.MethodPost, Path: "/settings/locales", Form: url.Values{action: {locale}}})
}

func UserContainersBulkActions() []webtemplates.BulkAction {
	return []webtemplates.BulkAction{
		{Key: "start", Label: "Start", Confirm: "Start the selected services?"},
		{Key: "stop", Label: "Stop", Confirm: "Stop the selected services?"},
		{Key: "restart", Label: "Restart", Confirm: "Restart the selected services?"},
		{Key: "cpu", Label: "Edit CPU", Confirm: "Set the CPU limit for the selected services:",
			Input: &webtemplates.BulkInput{Type: "number", Min: "0", Step: ".01", Placeholder: "cores", Hint: "0 = unlimited"}},
		{Key: "ram", Label: "Edit RAM", Confirm: "Set the memory limit (GB) for the selected services:",
			Input: &webtemplates.BulkInput{Type: "number", Min: "0", Step: ".01", Placeholder: "GB", Hint: "0 = unlimited"}},
		{Key: "pids", Label: "Edit PIDs", Confirm: "Set the max processes for the selected services:",
			Input: &webtemplates.BulkInput{Type: "number", Min: "0", Step: "1", Placeholder: "PIDs", Hint: "0 = unlimited"}},
	}
}

// userContainersBulkRoute replays the per-service forms on /users/<name>, items are compose service names
func userContainersBulkRoute(username string) BulkRoute {
	return func(action, value, service string) (*BulkCall, BulkResult) {
		if service == "" || strings.ContainsAny(service, "/?#") {
			return BulkSkip("Invalid service.")
		}
		call := BulkCall{Method: http.MethodPost, Path: "/containers/" + url.PathEscape(username) + "/" + action + "/" + url.PathEscape(service)}
		switch action {
		case "cpu", "ram", "pids":
			if f, err := strconv.ParseFloat(value, 64); err != nil || f < 0 {
				return BulkSkip("Limit must be 0 or a positive number.")
			}
			call.Form = url.Values{"value": {value}}
		}
		return BulkDo(call)
	}
}
