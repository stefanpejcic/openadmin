// This file implements installing, updating and deleting translation
// locales (fetched from a GitHub repo listing) and setting the default one.
package handlers

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
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

// Locales bundles the /settings/locales handler.
type Locales struct {
	Sessions *auth.Manager
}

// LocalesGithubURL / TranslationsDir / DefaultLocaleFilePath are the
// GitHub repo tree, local translations directory, and default-locale
// marker file used by the locale installer.
var (
	LocalesGithubURL      = "https://api.github.com/repos/stefanpejcic/openpanel-translations/git/trees/main?recursive=1"
	TranslationsDir       = "/etc/openpanel/openpanel/translations"
	DefaultLocaleFilePath = "/etc/openpanel/openpanel/default_locale"
)

// must match the key OpenPanel's i18n.AvailableLocales memoizes under
const localesCacheKey = "openpanel_cache_app.get_available_locales"

var localeFormatRe = regexp.MustCompile(`(?i)^[a-z]{2,3}-[a-z]{2,3}$`)

// only cc-cc dirs are locales, skips .github, scripts etc
var localeDirRe = regexp.MustCompile(`^[a-z]{2}-[a-z]{2}$`)

type githubContentItem struct {
	Name string
	Type string
	// git blob sha of <Name>/messages.po, empty if the dir has none
	Sha string
}

type localeRow struct {
	Locale          string `json:"locale"`
	Provider        string `json:"provider"`
	Path            string `json:"path"`
	Installed       bool   `json:"installed"`
	IsDefault       bool   `json:"is_default"`
	UpdateAvailable bool   `json:"update_available"`
	// FlagCode is render-only (not part of the JSON API response): the
	// last two characters of the locale name, used for the flag icon
	// filename.
	FlagCode  string `json:"-"`
	RemoteSha string `json:"-"`
}

// localesFetchFolders is injectable so tests never make a real GitHub API
// call, matching the caddyFetchMetrics/getDockerLogRun pattern used
// elsewhere.
var localesFetchFolders = func() ([]githubContentItem, int, error) {
	resp, err := http.Get(LocalesGithubURL)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Sha  string `json:"sha"`
		} `json:"tree"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return nil, resp.StatusCode, err
	}
	// one recursive tree call gives both the top-level dirs and each messages.po sha
	shas := map[string]string{}
	var items []githubContentItem
	for _, e := range tree.Tree {
		if e.Type == "tree" && !strings.Contains(e.Path, "/") {
			items = append(items, githubContentItem{Name: e.Path, Type: "dir"})
		}
		if dir, ok := strings.CutSuffix(e.Path, "/messages.po"); ok && e.Type == "blob" {
			shas[dir] = e.Sha
		}
	}
	for i := range items {
		items[i].Sha = shas[items[i].Name]
	}
	return items, resp.StatusCode, nil
}

// localesInstallRun is injectable so tests never shell out to a real
// opencli binary.
var localesInstallRun = func(locale string) error {
	return exec.Command("opencli", "locale", locale).Run()
}

// runs all locales in one opencli call, injectable for tests
var localesInstallAllRun = func(locales []string) error {
	return exec.Command("opencli", append([]string{"locale"}, locales...)...).Run()
}

// drops OpenPanel's cached locale list so installs/deletes show up right away, injectable for tests
var localesFlushCacheRun = func() {
	conn, err := net.DialTimeout("unix", FeaturesRedisSocketPath, 2*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return
	}
	respCommand(bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn)), "DEL", localesCacheKey)
}

// same hash git uses for blobs, so it compares directly with GitHub's sha
func gitBlobSha(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func localePoPath(locale string) string {
	return filepath.Join(TranslationsDir, localeBase(locale), "LC_MESSAGES", "messages.po")
}

func localeBase(locale string) string {
	return strings.SplitN(locale, "-", 2)[0]
}

func readDefaultLocale() string {
	if raw, err := os.ReadFile(DefaultLocaleFilePath); err == nil {
		if trimmed := strings.TrimSpace(string(raw)); trimmed != "" {
			return trimmed
		}
	}
	return "en"
}

// loadLocaleRows fetches the GitHub listing and cross-references it against what's installed locally.
func loadLocaleRows() ([]localeRow, string, error) {
	items, status, err := localesFetchFolders()
	if err != nil || status != http.StatusOK {
		return nil, "", fmt.Errorf("Failed to fetch data from GitHub: %d", status)
	}

	defaultLocaleBase := readDefaultLocale()
	results := []localeRow{}
	for _, item := range items {
		if item.Type != "dir" || !localeDirRe.MatchString(item.Name) {
			continue
		}
		baseName := localeBase(item.Name)
		localPath := filepath.Join(TranslationsDir, baseName)
		info, statErr := os.Stat(localPath)
		existsLocally := statErr == nil && info.IsDir()

		provider := "Community"
		if baseName == "en" {
			provider = "OpenPanel"
		}
		path := ""
		updateAvailable := false
		if existsLocally {
			path = localPath
			updateAvailable = item.Sha != "" && gitBlobSha(localePoPath(item.Name)) != item.Sha
		}
		flagCode := item.Name
		if len(flagCode) > 2 {
			flagCode = flagCode[len(flagCode)-2:]
		}
		results = append(results, localeRow{
			Locale:          item.Name,
			Provider:        provider,
			Path:            path,
			Installed:       existsLocally,
			IsDefault:       baseName == defaultLocaleBase,
			UpdateAvailable: updateAvailable,
			FlagCode:        flagCode,
			RemoteSha:       item.Sha,
		})
	}
	return results, defaultLocaleBase, nil
}

// localeActionResult is shared by the page and the API, which only differ in how they render it
type localeActionResult struct {
	Status  int
	OK      bool
	Message string
	// batch actions also report which locales went through
	Batch  bool
	Done   []string
	Failed []string
}

func localeFail(status int, message string) localeActionResult {
	return localeActionResult{Status: status, Message: message}
}

func localeOK(message string) localeActionResult {
	return localeActionResult{Status: http.StatusOK, OK: true, Message: message}
}

// runLocaleAction handles one POST action, checked in order: default, delete, update, install
func runLocaleAction(install, update, del, setDefault string) localeActionResult {
	switch {
	case setDefault != "":
		return setDefaultLocale(setDefault)
	case del != "":
		return deleteLocale(del)
	case update == "all":
		return batchLocales("Updated", "All locales are up to date.", true, func(row localeRow) bool { return row.UpdateAvailable })
	case update != "":
		if !localeFormatRe.MatchString(update) {
			return localeFail(http.StatusBadRequest, "Invalid locale format.")
		}
		return batchLocales("Updated", fmt.Sprintf("Locale '%s' is not installed.", update), false, func(row localeRow) bool {
			return row.Installed && strings.EqualFold(row.Locale, update)
		})
	case install == "all":
		return batchLocales("Installed", "No locales found on GitHub.", false, func(localeRow) bool { return true })
	case install != "":
		if !localeFormatRe.MatchString(install) {
			return localeFail(http.StatusBadRequest, "Invalid locale format.")
		}
		if err := localesInstallRun(install); err != nil {
			return localeFail(http.StatusInternalServerError, fmt.Sprintf("Failed to install locale: %s", err))
		}
		localesFlushCacheRun()
		return localeOK(fmt.Sprintf("Locale '%s' installed successfully.", install))
	}
	return localeFail(http.StatusBadRequest, "Missing 'locale', 'update', 'delete' or 'default' parameter.")
}

func setDefaultLocale(locale string) localeActionResult {
	if !localeFormatRe.MatchString(locale) {
		return localeFail(http.StatusBadRequest, "Invalid locale format.")
	}
	baseLocale := localeBase(locale)
	info, statErr := os.Stat(filepath.Join(TranslationsDir, baseLocale))
	if statErr != nil || !info.IsDir() {
		return localeFail(http.StatusBadRequest, fmt.Sprintf("Locale '%s' is not installed.", locale))
	}

	err := os.MkdirAll(filepath.Dir(DefaultLocaleFilePath), 0755)
	if err == nil {
		err = os.WriteFile(DefaultLocaleFilePath, []byte(baseLocale), 0644)
	}
	if err != nil {
		return localeFail(http.StatusInternalServerError, fmt.Sprintf("Failed to set default locale: %s", err))
	}
	return localeOK(fmt.Sprintf("Default locale set to '%s'.", baseLocale))
}

func deleteLocale(locale string) localeActionResult {
	if !localeFormatRe.MatchString(locale) {
		return localeFail(http.StatusBadRequest, "Invalid locale format.")
	}
	baseLocale := localeBase(locale)
	if baseLocale == readDefaultLocale() {
		return localeFail(http.StatusBadRequest, fmt.Sprintf("Locale '%s' is the default locale, set another one as default first.", locale))
	}
	localPath := filepath.Join(TranslationsDir, baseLocale)
	if info, err := os.Stat(localPath); err != nil || !info.IsDir() {
		return localeFail(http.StatusBadRequest, fmt.Sprintf("Locale '%s' is not installed.", locale))
	}
	if err := os.RemoveAll(localPath); err != nil {
		return localeFail(http.StatusInternalServerError, fmt.Sprintf("Failed to delete locale: %s", err))
	}
	localesFlushCacheRun()
	return localeOK(fmt.Sprintf("Locale '%s' deleted.", locale))
}

// batchLocales downloads every picked locale in one opencli call, then checks each file against GitHub's sha since opencli always exits 0
func batchLocales(verb, nothingMsg string, nothingOK bool, pick func(localeRow) bool) localeActionResult {
	rows, _, err := loadLocaleRows()
	if err != nil {
		return localeFail(http.StatusInternalServerError, err.Error())
	}
	var picked []localeRow
	var names []string
	for _, row := range rows {
		if pick(row) {
			picked = append(picked, row)
			names = append(names, row.Locale)
		}
	}
	if len(names) == 0 {
		if nothingOK {
			return localeOK(nothingMsg)
		}
		return localeFail(http.StatusBadRequest, nothingMsg)
	}
	if err := localesInstallAllRun(names); err != nil {
		return localeFail(http.StatusInternalServerError, fmt.Sprintf("Failed to run opencli: %s", err))
	}
	localesFlushCacheRun()

	res := localeActionResult{Status: http.StatusOK, Batch: true, Done: []string{}, Failed: []string{}}
	for _, row := range picked {
		localSha := gitBlobSha(localePoPath(row.Locale))
		if localSha != "" && (row.RemoteSha == "" || localSha == row.RemoteSha) {
			res.Done = append(res.Done, row.Locale)
		} else {
			res.Failed = append(res.Failed, row.Locale)
		}
	}
	res.OK = len(res.Failed) == 0
	res.Message = batchMessage(verb, res.Done, res.Failed)
	return res
}

func batchMessage(verb string, done, failed []string) string {
	msg := fmt.Sprintf("No locales were %s.", strings.ToLower(verb))
	if len(done) == 1 {
		msg = fmt.Sprintf("%s 1 locale: %s.", verb, done[0])
	} else if len(done) > 1 {
		msg = fmt.Sprintf("%s %d locales: %s.", verb, len(done), strings.Join(done, ", "))
	}
	if len(failed) > 0 {
		msg += fmt.Sprintf(" Failed: %s.", strings.Join(failed, ", "))
	}
	return msg
}

// ServeLocales handles GET/POST /settings/locales.
func (l *Locales) ServeLocales(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		l.handlePost(w, r)
		return
	}
	l.handleGet(w, r)
}

// handlePost accepts either a form-encoded or a JSON body: r.ParseForm()
// only populates r.PostForm for an urlencoded/multipart body, leaving a
// JSON request's body untouched, so this checks PostForm first and falls
// back to decoding a JSON body when the form is empty.
func (l *Locales) handlePost(w http.ResponseWriter, r *http.Request) {
	isJSONRequest := strings.Contains(r.Header.Get("Content-Type"), "application/json")

	fields := map[string]string{}
	r.ParseForm()
	if len(r.PostForm) > 0 {
		for _, k := range []string{"locale", "update", "delete", "default"} {
			fields[k] = r.PostFormValue(k)
		}
	} else {
		json.NewDecoder(r.Body).Decode(&fields)
	}

	res := runLocaleAction(fields["locale"], fields["update"], fields["delete"], fields["default"])
	if res.Status == http.StatusBadRequest {
		writeJSONError(w, res.Status, res.Message)
		return
	}
	if isJSONRequest {
		if res.OK {
			writeJSON(w, map[string]string{"message": res.Message})
		} else {
			writeJSONError(w, http.StatusInternalServerError, res.Message)
		}
		return
	}
	category := "success"
	if !res.OK {
		category = "error"
	}
	auth.AddFlash(w, r, l.Sessions, res.Message, category)
	http.Redirect(w, r, "/settings/locales", http.StatusSeeOther)
}

func (l *Locales) handleGet(w http.ResponseWriter, r *http.Request) {
	results, defaultLocaleBase, err := loadLocaleRows()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{
			"default_locale": defaultLocaleBase,
			"translations":   results,
		})
		return
	}

	var updates []string
	for _, row := range results {
		if row.UpdateAvailable {
			updates = append(updates, row.Locale)
		}
	}
	flashes := auth.PopFlashes(w, r, l.Sessions)
	if len(updates) > 0 {
		msg := fmt.Sprintf("Update available for %s.", updates[0])
		if len(updates) > 1 {
			msg = fmt.Sprintf("Updates available for %d locales: %s.", len(updates), strings.Join(updates, ", "))
		}
		flashes = append(flashes, auth.Flash{Category: "info", Message: msg})
	}

	webtemplates.Render(w, "settings_locales.html", mergeChrome(map[string]interface{}{
		"Translations":  results,
		"DefaultLocale": defaultLocaleBase,
		"UpdateCount":   len(updates),
		"CSRFToken":     csrf.Token(r),
		"Flashes":       flashes,
	}, r, "Locale Settings"))
}
