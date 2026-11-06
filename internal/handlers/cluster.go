// This file implements the /server/cluster page: enrolling servers on a master, and sync mode plus "Sync from master" on a slave.
package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/cluster"
	"openadmin/internal/webtemplates"
)

// Cluster bundles the /server/cluster handlers
type Cluster struct {
	Sessions *auth.Manager
	Manager  *cluster.Manager
	Licensed func() bool
}

const clusterPath = "/server/cluster"

func (c *Cluster) flashBack(w http.ResponseWriter, r *http.Request, msg, category string) {
	auth.AddFlash(w, r, c.Sessions, msg, category)
	http.Redirect(w, r, clusterPath, http.StatusSeeOther)
}

// SuperAdmin gates everything that changes the cluster, it reaches root on every server
func (c *Cluster) SuperAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u := auth.CurrentUser(r); u == nil || u.Role != "admin" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (c *Cluster) RequireLicense(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c.Licensed == nil || !c.Licensed() {
			c.flashBack(w, r, "Multi-server requires a valid Enterprise license.", "error")
			return
		}
		next(w, r)
	}
}

// ServePage handles GET /server/cluster
func (c *Cluster) ServePage(w http.ResponseWriter, r *http.Request) {
	st := c.Manager.State()
	data := map[string]interface{}{
		"Role":     st.Role,
		"Licensed": c.Licensed != nil && c.Licensed(),
		"JoinCode": r.URL.Query().Get("code"),
		"Invite":   r.URL.Query().Get("invite"),
		"NoTwoFA":  auth.CurrentUser(r) != nil && !auth.CurrentUser(r).TOTPEnabled,
		"Flashes":  auth.PopFlashes(w, r, c.Sessions),
	}
	switch st.Role {
	case cluster.RoleMaster:
		data["Health"] = c.Manager.Health()
		data["Nodes"] = st.Nodes
		data["Jobs"] = reverseJobs(st.Jobs)
		data["Unsynced"] = c.Manager.UnsyncedCount()
		accounts := c.Manager.Accounts()
		data["Accounts"] = accounts
		data["Servers"] = c.serverOptions(st)
	case cluster.RoleSlave:
		data["MasterName"] = st.MasterName
		data["MasterURL"] = st.MasterURL
		data["Mode"] = st.Mode
		data["Pinned"] = strings.Join(st.Pinned, "\n")
		data["GroupSecrets"] = groupOn(st.Groups, cluster.GroupSecrets)
		data["GroupNS"] = groupOn(st.Groups, cluster.GroupNameservers)
		data["LastSyncAt"] = st.LastSyncAt
		data["LastSyncMsg"] = st.LastSyncMsg
		changes, err := c.Manager.PreviewPull(nil)
		data["Changes"] = changes
		if err != nil {
			data["PreviewError"] = err.Error()
		}
	}
	webtemplates.Render(w, "server_cluster.html", mergeChrome(data, r, "Multi-Server"))
}

type clusterServerOption struct {
	ID       string
	Name     string
	Accounts []string
}

func (c *Cluster) serverOptions(st cluster.State) []clusterServerOption {
	accounts := c.Manager.Accounts()
	out := []clusterServerOption{{ID: cluster.LocalNode, Name: c.Manager.Hostname + " (master)", Accounts: accounts[cluster.LocalNode]}}
	for _, n := range st.Nodes {
		if n.Paused {
			continue
		}
		out = append(out, clusterServerOption{ID: n.ID, Name: n.Name, Accounts: accounts[n.ID]})
	}
	return out
}

func groupOn(groups []string, g string) bool {
	for _, x := range groups {
		if x == g {
			return true
		}
	}
	return false
}

func reverseJobs(jobs []*cluster.Job) []*cluster.Job {
	out := make([]*cluster.Job, len(jobs))
	for i, j := range jobs {
		out[len(jobs)-1-i] = j
	}
	return out
}

// ServeHealth handles GET /server/cluster/health, the dashboard widget polls it
func (c *Cluster) ServeHealth(w http.ResponseWriter, r *http.Request) {
	st := c.Manager.State()
	out := map[string]interface{}{"role": st.Role, "licensed": c.Licensed != nil && c.Licensed()}
	switch st.Role {
	case cluster.RoleMaster:
		out["servers"] = c.Manager.Health()
		out["unsynced"] = c.Manager.UnsyncedCount()
	case cluster.RoleSlave:
		out["master"] = st.MasterName
		out["master_url"] = st.MasterURL
		out["mode"] = st.Mode
		out["last_sync_at"] = st.LastSyncAt
		out["last_sync_msg"] = st.LastSyncMsg
	}
	writeJSON(w, out)
}

// HandleInvite handles POST /server/cluster/invite, the code goes into the install command of a new server
func (c *Cluster) HandleInvite(w http.ResponseWriter, r *http.Request) {
	code, err := c.Manager.GenerateInvite()
	if err != nil {
		c.flashBack(w, r, err.Error(), "error")
		return
	}
	http.Redirect(w, r, clusterPath+"?invite="+code+"#add", http.StatusSeeOther)
}

// HandleJoinCode handles POST /server/cluster/join-code
func (c *Cluster) HandleJoinCode(w http.ResponseWriter, r *http.Request) {
	if c.Manager.State().Role == cluster.RoleMaster {
		c.flashBack(w, r, "A master can't join another cluster.", "error")
		return
	}
	code, err := cluster.GenerateJoinCode(c.Manager.AdminPort)
	if err != nil {
		c.flashBack(w, r, err.Error(), "error")
		return
	}
	http.Redirect(w, r, clusterPath+"?code="+code, http.StatusSeeOther)
}

// HandleAdd handles POST /server/cluster/add
func (c *Cluster) HandleAdd(w http.ResponseWriter, r *http.Request) {
	n, err := c.Manager.AddNode(r.PostFormValue("url"), r.PostFormValue("name"), r.PostFormValue("code"))
	if err != nil {
		c.flashBack(w, r, "Could not add server: "+err.Error(), "error")
		return
	}
	c.flashBack(w, r, n.Name+" joined the cluster.", "success")
}

// HandleRemove handles POST /server/cluster/remove
func (c *Cluster) HandleRemove(w http.ResponseWriter, r *http.Request) {
	if err := c.Manager.RemoveNode(r.PostFormValue("id")); err != nil {
		c.flashBack(w, r, "Removed here, but the server could not be told: "+err.Error(), "warning")
		return
	}
	c.flashBack(w, r, "Server removed from the cluster.", "success")
}

func syncGroups(r *http.Request) []string {
	groups := []string{cluster.GroupCore}
	if r.PostFormValue("group_secrets") != "" {
		groups = append(groups, cluster.GroupSecrets)
	}
	if r.PostFormValue("group_ns") != "" {
		groups = append(groups, cluster.GroupNameservers)
	}
	return groups
}

// HandleSync handles POST /server/cluster/sync, pushing the master's settings to the chosen slaves
func (c *Cluster) HandleSync(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	ids := r.PostForm["nodes"]
	if len(ids) == 0 {
		c.flashBack(w, r, "Select at least one server to sync.", "warning")
		return
	}
	ok, failed := c.Manager.PushSettings(ids, syncGroups(r))
	var parts []string
	for name, res := range ok {
		parts = append(parts, name+": "+res.Message)
	}
	sort.Strings(parts)
	if len(failed) > 0 {
		for name, msg := range failed {
			parts = append(parts, name+" FAILED: "+msg)
		}
		c.flashBack(w, r, strings.Join(parts, " | "), "error")
		return
	}
	c.flashBack(w, r, strings.Join(parts, " | "), "success")
}

// ServeDiff handles GET /server/cluster/diff?node=<id>
func (c *Cluster) ServeDiff(w http.ResponseWriter, r *http.Request) {
	groups := []string{cluster.GroupCore}
	if r.URL.Query().Get("secrets") != "" {
		groups = append(groups, cluster.GroupSecrets)
	}
	if r.URL.Query().Get("ns") != "" {
		groups = append(groups, cluster.GroupNameservers)
	}
	changes, err := c.Manager.PreviewPush(r.URL.Query().Get("node"), groups)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"changes": changes})
}

// HandlePull handles POST /server/cluster/pull, the slave's "Sync from master"
func (c *Cluster) HandlePull(w http.ResponseWriter, r *http.Request) {
	res, err := c.Manager.Pull(syncGroups(r))
	if err != nil {
		c.flashBack(w, r, "Sync failed: "+err.Error(), "error")
		return
	}
	suffix := ""
	if res.Applied > 0 {
		suffix = " OpenPanel and OpenAdmin are restarting."
	}
	c.flashBack(w, r, res.Message+"."+suffix, "success")
}

// HandleSlaveSettings handles POST /server/cluster/settings
func (c *Cluster) HandleSlaveSettings(w http.ResponseWriter, r *http.Request) {
	var pinned []string
	for _, line := range strings.Split(r.PostFormValue("pinned"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			pinned = append(pinned, line)
		}
	}
	if err := c.Manager.SetSlaveSettings(r.PostFormValue("mode"), pinned, syncGroups(r)); err != nil {
		c.flashBack(w, r, err.Error(), "error")
		return
	}
	c.flashBack(w, r, "Sync settings saved.", "success")
}

// HandleRollback handles POST /server/cluster/rollback
func (c *Cluster) HandleRollback(w http.ResponseWriter, r *http.Request) {
	name, err := c.Manager.Rollback()
	if err != nil {
		c.flashBack(w, r, err.Error(), "error")
		return
	}
	c.flashBack(w, r, fmt.Sprintf("Settings restored from backup %s. OpenPanel and OpenAdmin are restarting.", name), "success")
}

// HandleLeave handles POST /server/cluster/leave
func (c *Cluster) HandleLeave(w http.ResponseWriter, r *http.Request) {
	if err := c.Manager.Leave(); err != nil {
		c.flashBack(w, r, err.Error(), "error")
		return
	}
	c.flashBack(w, r, "This server left the cluster and is standalone again.", "success")
}

// HandlePause handles POST /server/cluster/pause, a paused server is left out of everything until it's resumed
func (c *Cluster) HandlePause(w http.ResponseWriter, r *http.Request) {
	paused := r.PostFormValue("action") == "pause"
	if err := c.Manager.SetPaused(r.PostFormValue("id"), paused); err != nil {
		c.flashBack(w, r, err.Error(), "error")
		return
	}
	if paused {
		c.flashBack(w, r, "Server paused. Settings applied to all servers skip it until you resume it.", "success")
		return
	}
	c.flashBack(w, r, "Server resumed. Sync it to pick up settings changed while it was paused.", "success")
}

// HandlePromote handles POST /server/cluster/promote
func (c *Cluster) HandlePromote(w http.ResponseWriter, r *http.Request) {
	id := r.PostFormValue("id")
	name := ""
	for _, n := range c.Manager.State().Nodes {
		if n.ID == id {
			name = n.Name
		}
	}
	if err := c.Manager.Promote(id); err != nil {
		c.flashBack(w, r, "Could not change the master: "+err.Error(), "error")
		return
	}
	c.flashBack(w, r, name+" is now the master. Manage the cluster from its own OpenAdmin, logging in with that server's admin account.", "success")
}

// HandleUpdate handles POST /server/cluster/update
func (c *Cluster) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	if err := c.Manager.StartUpdate(r.PostFormValue("id")); err != nil {
		c.flashBack(w, r, "Could not start the update: "+err.Error(), "error")
		return
	}
	c.flashBack(w, r, "Update started. That server restarts its services when it finishes, it can show as offline meanwhile.", "success")
}

// HandleMove handles POST /server/cluster/move
func (c *Cluster) HandleMove(w http.ResponseWriter, r *http.Request) {
	job, err := c.Manager.StartMove(r.PostFormValue("account"), r.PostFormValue("from"), r.PostFormValue("to"), r.PostFormValue("live") != "", r.PostFormValue("reseller"))
	if err != nil {
		c.flashBack(w, r, "Move refused: "+err.Error(), "error")
		return
	}
	c.flashBack(w, r, "Moving "+job.Account+" from "+job.FromName+" to "+job.ToName+". Progress is shown below.", "success")
}

// HandleMoveFinish handles POST /server/cluster/move/{id}/{action}
func (c *Cluster) HandleMoveFinish(w http.ResponseWriter, r *http.Request) {
	var err error
	msg := ""
	switch r.PathValue("action") {
	case "finalize":
		err = c.Manager.FinalizeMove(r.PathValue("id"))
		msg = "Account removed from the source server, the move is complete."
	case "cancel":
		err = c.Manager.CancelMove(r.PathValue("id"))
		msg = "Copy removed from the destination server, the account runs on the source again."
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		c.flashBack(w, r, err.Error(), "error")
		return
	}
	c.flashBack(w, r, msg, "success")
}
