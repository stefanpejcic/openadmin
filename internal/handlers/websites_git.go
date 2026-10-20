package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"openadmin/internal/activity"
	"openadmin/internal/auth"
	"openadmin/internal/podman"
)

// WebsitesGitHome is where OpenPanel keeps each account's repos.json, /home/<context>/git
var WebsitesGitHome = "/home"

// gitRepoFile is the part of OpenPanel's repos.json the websites page shows, secrets are never decoded
type gitRepoFile struct {
	Name         string `json:"name"`
	Source       string `json:"source"`
	Branch       string `json:"branch"`
	TargetDir    string `json:"target_dir"`
	DeployMode   string `json:"deploy_mode"`
	LastDeploy   string `json:"last_deploy"`
	LastDeployed string `json:"last_deployed_commit"`
	LastError    string `json:"last_error"`
}

// gitRepo is one repository deploying into a website, as the Git column shows it
type gitRepo struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Branch      string `json:"branch"`
	Mode        string `json:"deploy_mode"`
	TargetDir   string `json:"target_dir"`
	Commit      string `json:"commit"`
	DeployedAgo string `json:"deployed_ago"`
	Deployed    string `json:"deployed"`
	Error       string `json:"error,omitempty"`
}

func (g gitRepo) ModeLabel() string {
	switch g.Mode {
	case "automatic":
		return "Automatic"
	case "manual":
		return "Manual"
	}
	return "No deployment"
}

// readGitRepos loads an account's repositories, an account without the git module just has none
func readGitRepos(userContext string) []gitRepoFile {
	if userContext == "" || strings.ContainsAny(userContext, "/\\") {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(WebsitesGitHome, userContext, "git", "repos.json"))
	if err != nil {
		return nil
	}
	var repos []gitRepoFile
	if json.Unmarshal(data, &repos) != nil {
		return nil
	}
	return repos
}

// gitReposForSite keeps the repositories that deploy into the site's docroot or a folder under it
func gitReposForSite(repos []gitRepoFile, docroot string, now time.Time) []gitRepo {
	docroot = strings.TrimRight(docroot, "/")
	if docroot == "" {
		return nil
	}
	var out []gitRepo
	for _, r := range repos {
		target := strings.TrimRight(r.TargetDir, "/")
		if target != docroot && !strings.HasPrefix(target, docroot+"/") {
			continue
		}
		g := gitRepo{Name: r.Name, Source: r.Source, Branch: r.Branch, Mode: r.DeployMode, TargetDir: r.TargetDir, Deployed: r.LastDeploy, Error: r.LastError}
		if len(r.LastDeployed) >= 7 {
			g.Commit = r.LastDeployed[:7]
		}
		if t, err := time.Parse("2006-01-02 15:04:05 UTC", r.LastDeploy); err == nil {
			g.DeployedAgo = timeSince(t.Local().Format("2006-01-02 15:04:05"), now)
		}
		out = append(out, g)
	}
	return out
}

// GitRemote is true when at least one of the site's repositories can be pulled
func (s websiteRow) GitRemote() bool {
	for _, g := range s.Git {
		if g.Source == "remote" {
			return true
		}
	}
	return false
}

// GitDeployable is true when at least one of the site's repositories deploys
func (s websiteRow) GitDeployable() bool {
	for _, g := range s.Git {
		if g.Mode != "none" {
			return true
		}
	}
	return false
}

// GitErrors joins the last errors of every repository of the site, so a failing second repo still shows
func (s websiteRow) GitErrors() string {
	var errs []string
	for _, g := range s.Git {
		if g.Error != "" {
			errs = append(errs, g.Name+": "+g.Error)
		}
	}
	return strings.Join(errs, "; ")
}

// GitOthers lists the repositories after the first one, for the "+N more" hover
func (s websiteRow) GitOthers() string {
	if len(s.Git) < 2 {
		return ""
	}
	names := make([]string, 0, len(s.Git)-1)
	for _, g := range s.Git[1:] {
		names = append(names, g.Name+" ("+g.Branch+")")
	}
	return strings.Join(names, ", ")
}

// BulkSkip is the bulk actions this row can't run
func (s websiteRow) BulkSkip() string {
	var skip []string
	if !s.IsWordPress() {
		skip = append(skip, "vulnerability")
	}
	if !s.GitRemote() {
		skip = append(skip, "git_pull")
	}
	if !s.GitDeployable() {
		skip = append(skip, "git_deploy")
	}
	return strings.Join(skip, ",")
}

// GitModuleEnabled is gitModuleEnabled for main.go's bulk route
func GitModuleEnabled() bool { return gitModuleEnabled() }

func gitModuleEnabled() bool {
	modules := modulesEnabledList(ModulesConfigFilePath)
	for _, m := range modules {
		if strings.TrimSpace(m) == "git" {
			return true
		}
	}
	return false
}

// websitesGitRun runs one repo action through the panel's own git code inside the openpanel container, a var so tests don't need podman
var websitesGitRun = func(action, userContext, repo string) (string, error) {
	cmd, err := podman.Command("default", "exec", "openpanel", "/openpanel", "git", action, userContext, repo)
	if err != nil {
		return "", err
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// siteGit loads one site with its owner's context and docroot, enough to find its repositories
func (ws *Websites) siteGit(idStr string) (websiteRow, bool) {
	id, err := strconv.Atoi(idStr)
	if err != nil || ws.MySQL == nil {
		return websiteRow{}, false
	}
	var s websiteRow
	var docroot, userContext sql.NullString
	err = ws.MySQL.QueryRow(`
		SELECT s.id, s.site_name, d.docroot, u.server
		FROM sites s
		LEFT JOIN domains d ON d.domain_id = s.domain_id
		LEFT JOIN users u ON u.id = d.user_id
		WHERE s.id = ?`, id).Scan(&s.ID, &s.SiteName, &docroot, &userContext)
	if err != nil {
		return websiteRow{}, false
	}
	s.Docroot, s.Context = docroot.String, userContext.String
	if _, folder, ok := strings.Cut(s.SiteName, "/"); ok && folder != "" && s.Docroot != "" {
		s.Docroot = strings.TrimRight(s.Docroot, "/") + "/" + folder
	}
	s.Git = gitReposForSite(readGitRepos(s.Context), s.Docroot, time.Now())
	return s, true
}

// HandleGit handles POST /websites/git/{action}, pulls or deploys every repository of one site
func (ws *Websites) HandleGit(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "pull" && action != "deploy" {
		http.NotFound(w, r)
		return
	}
	back := "/websites"
	fail := func(msg string) {
		activity.Fail(r.Context())
		auth.AddFlash(w, r, ws.Sessions, msg, "error")
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
	site, ok := ws.siteGit(r.FormValue("id"))
	if !ok {
		fail("Website not found.")
		return
	}
	if !gitModuleEnabled() {
		fail("The Git module is not enabled.")
		return
	}

	var done, failed []string
	for _, g := range site.Git {
		if action == "pull" && g.Source != "remote" {
			continue
		}
		if action == "deploy" && g.Mode == "none" {
			continue
		}
		if out, err := websitesGitRun(action, site.Context, g.Name); err != nil {
			if out == "" {
				out = err.Error()
			}
			failed = append(failed, out)
		} else {
			done = append(done, g.Name)
		}
	}

	verb := map[string]string{"pull": "Pulled", "deploy": "Deployed"}[action]
	switch {
	case len(done) == 0 && len(failed) == 0:
		fail("No repositories to " + action + " on " + site.SiteName + ".")
		return
	case len(failed) > 0:
		activity.Fail(r.Context())
		auth.AddFlash(w, r, ws.Sessions, strings.Join(failed, " "), "error")
	default:
		activity.Describe(r.Context(), verb+" Git repositories "+strings.Join(done, ", ")+" for "+site.SiteName)
		auth.AddFlash(w, r, ws.Sessions, verb+" "+strings.Join(done, ", ")+" on "+site.SiteName+".", "success")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// gitBulkRoute sends a bulk Git action through the single-site route
func gitBulkRoute(site websiteRow, action, item string) (*BulkCall, BulkResult) {
	path := "/websites/git/" + strings.TrimPrefix(action, "git_")
	return BulkDo(BulkCall{Label: site.SiteName, Method: http.MethodPost, Path: path, Form: url.Values{"id": {item}}})
}
