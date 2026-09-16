// This file implements the plugin store: browsing the first-party plugin
// list published at github.com/stefanpejcic/OpenPanel/tree/main/plugins (one
// git submodule per plugin, so the actual code lives in each plugin's own
// repo - this monorepo folder is just an index), and installing a plugin
// from either a store entry, an arbitrary git URL, or an uploaded zip.
package handlers

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// PluginStore bundles the /settings/modules/store handlers.
type PluginStore struct {
	Sessions *auth.Manager
}

// storeIndexAPI lists the plugin store's index folder - each entry is a git submodule, so GitHub's contents API reports it with type "submodule" and a submodule_git_url pointing at the plugin's real repo.
const storeIndexAPI = "https://api.github.com/repos/stefanpejcic/OpenPanel/contents/plugins"

// storeHTTPClient is used for both the GitHub API call and the readme.txt fetch - short timeout since this blocks a page render.
var storeHTTPClient = &http.Client{Timeout: 10 * time.Second}

// StoreItem is one plugin listed in the store, merged with whether it's already installed locally.
type StoreItem struct {
	Name        string
	GitURL      string
	Title       string
	Description string
	Version     string
	Author      string
	HelpLink    string
	Installed   bool
}

type githubContentEntry struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	SHA             string `json:"sha"`
	SubmoduleGitURL string `json:"submodule_git_url"`
}

// fetchStoreIndex lists the available plugins from the store index and enriches each with its own readme.txt - fetched from the plugin's own repo (not this monorepo), pinned to the exact commit the submodule points at so the listing can't be raced by a later push to the plugin's repo.
func fetchStoreIndex(ctx context.Context) ([]githubContentEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, storeIndexAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := storeHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("store index request returned HTTP %d", resp.StatusCode)
	}

	var entries []githubContentEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// parseGitHubOwnerRepo extracts "owner", "repo" from a "https://github.com/owner/repo(.git)" URL.
func parseGitHubOwnerRepo(gitURL string) (owner, repo string, ok bool) {
	const prefix = "https://github.com/"
	if !strings.HasPrefix(gitURL, prefix) {
		return "", "", false
	}
	rest := strings.TrimSuffix(strings.TrimRight(strings.TrimPrefix(gitURL, prefix), "/"), ".git")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// fetchPluginReadme fetches a plugin's own readme.txt straight from its repo, pinned to commitSHA.
func fetchPluginReadme(ctx context.Context, gitURL, commitSHA string) (map[string]string, error) {
	owner, repo, ok := parseGitHubOwnerRepo(gitURL)
	if !ok {
		return nil, errors.New("unrecognized git host")
	}
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/readme.txt", owner, repo, commitSHA)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := storeHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("readme.txt request returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, err
	}
	return parsePluginReadmeBytes(body), nil
}

// ServeStore handles GET /settings/modules/store.
func (p *PluginStore) ServeStore(w http.ResponseWriter, r *http.Request) {
	installed := map[string]bool{}
	for _, plugin := range getAllPlugins(ModulesPluginsBaseDir) {
		installed[plugin["folder"]] = true
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	entries, err := fetchStoreIndex(ctx)
	var items []StoreItem
	var storeError string
	if err != nil {
		storeError = "Could not reach the plugin store: " + err.Error()
	}
	for _, e := range entries {
		if e.Type != "submodule" || e.SubmoduleGitURL == "" {
			continue
		}
		item := StoreItem{Name: e.Name, GitURL: e.SubmoduleGitURL, Installed: installed[e.Name]}
		if meta, err := fetchPluginReadme(ctx, e.SubmoduleGitURL, e.SHA); err == nil {
			item.Title = meta["title"]
			item.Description = meta["description"]
			item.Version = meta["version"]
			item.Author = meta["author"]
			item.HelpLink = meta["help_link"]
		}
		items = append(items, item)
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{"plugins": items, "error": storeError})
		return
	}

	webtemplates.Render(w, "settings_plugin_store.html", mergeChrome(map[string]interface{}{
		"Plugins":    items,
		"StoreError": storeError,
		"CSRFToken":  csrf.Token(r),
		"Flashes":    auth.PopFlashes(w, r, p.Sessions),
	}, r, "Plugin Store"))
}

// pluginNameRE is the safe charset for a plugin's folder/binary name, matching the existing FTP/domain-style allowlists used elsewhere in the codebase for values that end up in filesystem paths.
var pluginNameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// httpsGitURLRE requires a plain https:// URL - deliberately excludes git's other transport forms (ext::, fd::, file://, ssh with a bare host, etc.) since several of those can be abused to run arbitrary local commands via a crafted "URL".
var httpsGitURLRE = regexp.MustCompile(`^https://[a-zA-Z0-9.-]+(/[a-zA-Z0-9._~%!$&'()*+,;=:@-]+)+/?$`)

// blockedGitHosts rejects the obvious local/metadata targets - this is an admin-only feature so it's not a hard security boundary, just a guard against an admin pasting a URL that resolves inward by mistake.
var blockedGitHosts = map[string]bool{
	"localhost":                true,
	"127.0.0.1":                true,
	"0.0.0.0":                  true,
	"169.254.169.254":          true,
	"metadata.google.internal": true,
	"::1":                      true,
}

// derivePluginName extracts a filesystem-safe plugin name from the last path segment of a git URL.
func derivePluginName(gitURL string) (string, bool) {
	trimmed := strings.TrimSuffix(strings.TrimRight(gitURL, "/"), ".git")
	idx := strings.LastIndex(trimmed, "/")
	if idx == -1 || idx == len(trimmed)-1 {
		return "", false
	}
	name := trimmed[idx+1:]
	if !pluginNameRE.MatchString(name) {
		return "", false
	}
	return name, true
}

// validateGitURL checks gitURL is a plausible, non-local https:// URL.
func validateGitURL(gitURL string) error {
	if !httpsGitURLRE.MatchString(gitURL) {
		return errors.New("only plain https:// URLs are supported")
	}
	host := gitURL[len("https://"):]
	if idx := strings.IndexAny(host, "/:"); idx != -1 {
		host = host[:idx]
	}
	if blockedGitHosts[strings.ToLower(host)] {
		return errors.New("that host isn't allowed")
	}
	return nil
}

// installPluginFromGit clones gitURL into ModulesPluginsBaseDir/<name>, validates it's a real plugin (has a readme.txt), and makes its own binary executable if present. Returns the installed plugin's name.
func installPluginFromGit(ctx context.Context, gitURL string) (string, error) {
	if err := validateGitURL(gitURL); err != nil {
		return "", err
	}
	name, ok := derivePluginName(gitURL)
	if !ok {
		return "", errors.New("could not derive a plugin name from that URL")
	}
	target := filepath.Join(ModulesPluginsBaseDir, name)
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("a plugin folder named %q already exists", name)
	}

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// "--" stops git from treating a URL that happens to start with "-" as a flag.
	cmd := exec.CommandContext(cctx, "git", "clone", "--depth=1", "--", gitURL, target)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(target)
		return "", fmt.Errorf("git clone failed: %s", strings.TrimSpace(string(out)))
	}

	return name, finalizeInstalledPlugin(target, name)
}

// finalizeInstalledPlugin validates a freshly-installed plugin folder has a readme.txt, drops any .git metadata, runs the plugin's own install.sh if it ships one (e.g. to pick the right binary for this server's CPU architecture - see the plugin boilerplate's install.sh), and makes the plugin's own binary (if present either way) executable.
func finalizeInstalledPlugin(target, name string) error {
	if _, err := os.Stat(filepath.Join(target, "readme.txt")); err != nil {
		os.RemoveAll(target)
		return errors.New("not a valid plugin: missing readme.txt")
	}
	os.RemoveAll(filepath.Join(target, ".git"))

	if installScript := filepath.Join(target, "install.sh"); fileExists(installScript) {
		cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, "bash", "install.sh")
		cmd.Dir = target
		if out, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(target)
			return fmt.Errorf("install.sh failed: %s", strings.TrimSpace(string(out)))
		}
	}

	if info, err := os.Stat(filepath.Join(target, name)); err == nil && !info.IsDir() {
		_ = os.Chmod(filepath.Join(target, name), 0o755)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// InstallFromGit handles POST /settings/modules/store/install - used both for installing a store entry (its exact git_url is submitted by the page) and for installing a custom plugin from an arbitrary git URL the admin pastes in.
func (p *PluginStore) InstallFromGit(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	gitURL := strings.TrimSpace(r.PostFormValue("git_url"))
	if gitURL == "" {
		auth.AddFlash(w, r, p.Sessions, "Please provide a git URL.", "danger")
		http.Redirect(w, r, "/settings/modules/store", http.StatusFound)
		return
	}

	name, err := installPluginFromGit(r.Context(), gitURL)
	if err != nil {
		auth.AddFlash(w, r, p.Sessions, "Could not install plugin: "+err.Error(), "danger")
		http.Redirect(w, r, "/settings/modules/store", http.StatusFound)
		return
	}

	auth.AddFlash(w, r, p.Sessions, "Plugin \""+name+"\" installed successfully.", "success")
	http.Redirect(w, r, "/settings/modules", http.StatusFound)
}

// maxPluginUploadBytes bounds both the multipart form and the decompressed size of an uploaded plugin archive, so a hostile zip (small on disk, huge decompressed - a "zip bomb") can't fill the disk.
const maxPluginUploadBytes = 64 << 20 // 64MB

// InstallFromUpload handles POST /settings/modules/store/upload - installs a plugin from a .zip the admin uploads from their own device.
func (p *PluginStore) InstallFromUpload(w http.ResponseWriter, r *http.Request) {
	redirectWithError := func(msg string) {
		auth.AddFlash(w, r, p.Sessions, msg, "danger")
		http.Redirect(w, r, "/settings/modules/store", http.StatusFound)
	}

	if err := r.ParseMultipartForm(maxPluginUploadBytes); err != nil {
		redirectWithError("Upload too large or malformed.")
		return
	}
	file, header, err := r.FormFile("plugin_archive")
	if err != nil {
		redirectWithError("Please choose a .zip file to upload.")
		return
	}
	defer file.Close()

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		redirectWithError("Only .zip archives are supported.")
		return
	}

	name, err := installPluginFromUpload(file, header.Size)
	if err != nil {
		redirectWithError("Could not install plugin: " + err.Error())
		return
	}

	auth.AddFlash(w, r, p.Sessions, "Plugin \""+name+"\" installed successfully.", "success")
	http.Redirect(w, r, "/settings/modules", http.StatusFound)
}

// installPluginFromUpload extracts a zip into a temporary staging folder, requires a single top-level directory (the plugin's own folder name), and moves it into place. Returns the installed plugin's name.
func installPluginFromUpload(file io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(file, size)
	if err != nil {
		return "", errors.New("not a valid zip file")
	}

	rootName, err := zipSingleRootDir(zr)
	if err != nil {
		return "", err
	}
	if !pluginNameRE.MatchString(rootName) {
		return "", fmt.Errorf("invalid plugin folder name %q in archive", rootName)
	}

	target := filepath.Join(ModulesPluginsBaseDir, rootName)
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("a plugin folder named %q already exists", rootName)
	}

	stagingDir, err := os.MkdirTemp(ModulesPluginsBaseDir, ".upload-*")
	if err != nil {
		return "", errors.New("could not create a staging directory")
	}
	defer os.RemoveAll(stagingDir)

	var extracted int64
	for _, f := range zr.File {
		destPath, ok := safeZipEntryPath(stagingDir, f.Name)
		if !ok {
			return "", fmt.Errorf("archive entry %q escapes the plugin folder", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return "", err
		}
		extracted, err = extractZipFile(f, destPath, extracted)
		if err != nil {
			return "", err
		}
	}

	stagedRoot := filepath.Join(stagingDir, rootName)
	if err := os.Rename(stagedRoot, target); err != nil {
		return "", errors.New("could not move plugin into place")
	}

	return rootName, finalizeInstalledPlugin(target, rootName)
}

// zipSingleRootDir requires every entry in zr share the same top-level directory (the plugin's folder), returning that name.
func zipSingleRootDir(zr *zip.Reader) (string, error) {
	root := ""
	for _, f := range zr.File {
		cleaned := strings.TrimPrefix(filepath.Clean(f.Name), "/")
		first, _, _ := strings.Cut(cleaned, "/")
		if first == "" || first == "." || first == ".." {
			return "", errors.New("archive must contain a single top-level plugin folder")
		}
		if root == "" {
			root = first
		} else if root != first {
			return "", errors.New("archive must contain a single top-level plugin folder")
		}
	}
	if root == "" {
		return "", errors.New("archive is empty")
	}
	return root, nil
}

// safeZipEntryPath resolves a zip entry name against baseDir and confirms the result stays inside it (zip-slip protection).
func safeZipEntryPath(baseDir, name string) (string, bool) {
	cleaned := strings.TrimPrefix(filepath.Clean("/" + name), "/")
	dest := filepath.Join(baseDir, cleaned)
	if dest != baseDir && !strings.HasPrefix(dest, baseDir+string(os.PathSeparator)) {
		return "", false
	}
	return dest, true
}

// extractZipFile writes one zip entry to destPath, enforcing the running extracted-bytes total stays under maxPluginUploadBytes (zip-bomb protection).
func extractZipFile(f *zip.File, destPath string, extractedSoFar int64) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return extractedSoFar, err
	}
	defer rc.Close()

	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return extractedSoFar, err
	}
	defer out.Close()

	remaining := maxPluginUploadBytes - extractedSoFar
	if remaining <= 0 {
		return extractedSoFar, errors.New("archive exceeds the maximum allowed size")
	}
	n, err := io.Copy(out, io.LimitReader(rc, remaining+1))
	if err != nil {
		return extractedSoFar, err
	}
	if n > remaining {
		return extractedSoFar, errors.New("archive exceeds the maximum allowed size")
	}
	return extractedSoFar + n, nil
}
