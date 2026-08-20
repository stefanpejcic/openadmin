// This file implements the /server/podman page: podman info, table listings
// for images/volumes/networks, and (for images) management -- per-image
// delete/pull, plus page-wide "pull every missing stack image" / "delete
// every unused image" bulk actions. The images table also cross-references
// the new-user compose stack (podmanStackImages) so an image that stack
// declares but hasn't been pulled into the shared store yet still shows up,
// flagged as not-downloaded with a one-click Pull. All of these run
// asynchronously with the browser polling ServePodmanImageActionStatus /
// ServePodmanImagesBulkStatus, mirroring the services table's
// pendingServiceActions pattern in services.go. Volumes/networks management
// and compose actions beyond this are still out of scope -- display only.
package handlers

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/csrf"
	"gopkg.in/yaml.v3"

	"openadmin/internal/auth"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// Podman bundles the /server/podman handler.
type Podman struct {
	Sessions *auth.Manager
	MySQL    *sql.DB
}

// podmanRunContextRun is injectable so tests never shell out to the real
// podman binary. It runs `podman <args...>` against context (root's local
// stack for "default", or a given hosting user's own rootless instance)
// and returns combined stdout+stderr.
var podmanRunContextRun = func(context string, args ...string) (string, error) {
	cmd, err := podman.Command(context, args...)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	return out.String(), err
}

// podmanRunRun is podmanRunContextRun pinned to root's own local ("default")
// context -- what the Info/Images/Volumes/Networks tabs themselves list.
var podmanRunRun = func(args ...string) (string, error) {
	return podmanRunContextRun("default", args...)
}

// podmanRunRunStdout is like podmanRunRun but returns stdout alone,
// discarding stderr entirely rather than merging it in. Use this (not
// podmanRunRun) for any call whose stdout is parsed as JSON/a single
// structured value -- podman sometimes writes a benign warning line to
// stderr on an otherwise-successful call (e.g. "manifest ... is not a
// manifest list but a single image" from `manifest inspect`), which
// corrupts the JSON if it ends up merged into the same buffer.
var podmanRunRunStdout = func(args ...string) (string, error) {
	cmd, err := podman.Command("default", args...)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	return out.String(), err
}

// podmanInfoText returns the raw `podman info` output, same as a user would
// see running it in a terminal. On failure, the captured output (which for
// podman usually still contains a useful error message) is shown as-is.
func podmanInfoText() string {
	out, err := podmanRunRun("info")
	if err != nil && strings.TrimSpace(out) == "" {
		return "Failed to get podman info: " + err.Error()
	}
	return out
}

type podmanImageRow struct {
	ID               string // short, display-only
	FullID           string // used for the delete action
	Repository       string
	Tag              string
	Size             string
	SizeBytes        float64 // raw value backing Size, for numeric sort
	Created          string
	SystemContainers int  // containers in root's own local podman
	UserContainers   int  // containers across every hosting user's rootless podman
	NotDownloaded    bool // referenced by the new-user stack, but not yet pulled

	// Update-check state, populated from podmanUpdateStatusCache (never
	// checked automatically -- see podmanCheckImageUpdateRun's doc comment
	// for why). UpdateChecked is false until a per-row or bulk check has
	// actually run for this ref.
	UpdateChecked   bool
	UpdateAvailable bool

	// Vulnerability-check state, populated from podmanVulnStatusCache --
	// same on-demand-only model as the update check above (see
	// podmanCheckImageVulnerabilitiesRun's doc comment), but there's no
	// per-row check here, only the bulk "Check all for vulnerabilities"
	// action, since a Trivy scan is much slower than a digest comparison.
	VulnChecked bool
	VulnCount   int
}

type podmanVolumeRow struct {
	Name       string
	Driver     string
	Mountpoint string
	Created    string
}

type podmanNetworkRow struct {
	ID      string
	Name    string
	Driver  string
	Subnet  string
	Created string
}

// podmanShortID trims a full container/image ID down to the 12-char form
// podman's own CLI output uses.
func podmanShortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// podmanStringField reads a string field from a decoded JSON object,
// tolerating a handful of alternate casings podman has used across
// versions (e.g. "Id" vs "ID").
func podmanStringField(obj map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := obj[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// podmanFormatCreated renders a podman "Created" field as
// "yyyy-mm-dd hh:mm:ss". Different podman versions emit this as either a
// unix timestamp (number) or an RFC3339 string.
func podmanFormatCreated(v interface{}) string {
	switch val := v.(type) {
	case float64:
		return time.Unix(int64(val), 0).Format("2006-01-02 15:04:05")
	case string:
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			return t.Format("2006-01-02 15:04:05")
		}
		return val
	default:
		return ""
	}
}

// podmanFormatSize renders a byte count as a human-readable string (e.g.
// "128.4 MB"), matching the scale podman's own CLI uses.
func podmanFormatSize(v interface{}) string {
	f, ok := v.(float64)
	if !ok {
		return ""
	}
	const unit = 1000.0
	if f < unit {
		return strconv.FormatFloat(f, 'f', 0, 64) + " B"
	}
	div, exp := unit, 0
	for n := f / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"kB", "MB", "GB", "TB", "PB"}
	if exp >= len(units) {
		exp = len(units) - 1
	}
	return strconv.FormatFloat(f/div, 'f', 1, 64) + " " + units[exp]
}

// podmanSplitRef splits a "repo:tag" (or "registryhost:port/repo:tag")
// image reference on the last ":" after the last "/", so a registry
// host:port prefix isn't mistaken for the tag separator. An empty ref, or
// one with no tag segment, returns "<none>" for the missing part(s),
// matching the CLI's own convention for a dangling/untagged image.
func podmanSplitRef(name string) (repo, tag string) {
	if name == "" {
		return "<none>", "<none>"
	}
	slash := strings.LastIndex(name, "/")
	if idx := strings.LastIndex(name[slash+1:], ":"); idx != -1 {
		idx += slash + 1
		return name[:idx], name[idx+1:]
	}
	return name, "<none>"
}

// podmanImageRepoTag extracts a "repository", "tag" pair for an image list
// entry. RepoTags is the field podman's own `podman images` output is built
// from, but some podman versions report it as null for images that only
// have RepoDigests set and instead carry the human-readable name(s) in
// Names -- so Names is tried as a fallback.
func podmanImageRepoTag(item map[string]interface{}) (repo, tag string) {
	name := ""
	if tags, ok := item["RepoTags"].([]interface{}); ok && len(tags) > 0 {
		if s, ok := tags[0].(string); ok && s != "" {
			name = s
		}
	}
	if name == "" {
		if names, ok := item["Names"].([]interface{}); ok && len(names) > 0 {
			if s, ok := names[0].(string); ok && s != "" {
				name = s
			}
		}
	}
	return podmanSplitRef(name)
}

// podmanNormalizeRef canonicalizes an image reference the same way podman
// itself does when it stores/lists an image, so a stack ref parsed straight
// out of the compose file (e.g. "shinsenter/php:7.2-fpm", "nginx:alpine")
// can be compared against what `podman images` reports (which is always
// fully qualified, e.g. "docker.io/shinsenter/php:7.2-fpm",
// "docker.io/library/nginx:alpine") without every already-downloaded image
// falsely showing up as "not downloaded" too.
func podmanNormalizeRef(ref string) string {
	if ref == "" {
		return ref
	}
	firstSlash := strings.Index(ref, "/")
	if firstSlash == -1 {
		return "docker.io/library/" + ref
	}
	maybeDomain := ref[:firstSlash]
	if maybeDomain == "localhost" || strings.ContainsAny(maybeDomain, ".:") {
		// Already has an explicit registry host (possibly with a port).
		return ref
	}
	return "docker.io/" + ref
}

// podmanNormalizeAndDedupRefs applies podmanNormalizeRef to every ref, then
// dedupes and sorts.
func podmanNormalizeAndDedupRefs(refs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		norm := podmanNormalizeRef(ref)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	sort.Strings(out)
	return out
}

// podmanStackComposePath is the compose file used to provision each new
// hosting user's container stack (see install.sh's shared-image-prefetch
// step). Every image it declares is meant to already be in the shared
// store by the time a new user is created, so the Images tab can flag any
// that are missing -- e.g. a version bump to this file was never followed
// by a prefetch -- with a one-click Pull.
const podmanStackComposePath = "/etc/openpanel/docker/compose/1.0/docker-compose.yml"

var (
	composeImageLineRe  = regexp.MustCompile(`(?m)^\s*image:\s*(\S+)`)
	composeVarDefaultRe = regexp.MustCompile(`\$\{[A-Za-z0-9_]+:-([^}]*)\}`)
)

// podmanParseComposeImages extracts every service's "image:" value from
// resolved `podman-compose config` YAML output.
func podmanParseComposeImages(yamlBytes []byte) []string {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(yamlBytes, &doc); err != nil {
		return nil
	}
	services, _ := doc["services"].(map[string]interface{})
	seen := map[string]bool{}
	var images []string
	for _, svc := range services {
		svcMap, ok := svc.(map[string]interface{})
		if !ok {
			continue
		}
		img, _ := svcMap["image"].(string)
		if img == "" || seen[img] {
			continue
		}
		seen[img] = true
		images = append(images, img)
	}
	sort.Strings(images)
	return images
}

// podmanParseComposeImagesRaw extracts "image:" lines directly from the raw
// compose file, resolving simple "${VAR:-default}" substitutions the same
// way install.sh's own fallback grep does, and skipping any line whose
// variable has no default (unresolvable without the install-time env).
func podmanParseComposeImagesRaw(raw string) []string {
	seen := map[string]bool{}
	var images []string
	for _, m := range composeImageLineRe.FindAllStringSubmatch(raw, -1) {
		img := strings.Trim(m[1], `"'`)
		img = composeVarDefaultRe.ReplaceAllString(img, "$1")
		if img == "" || strings.Contains(img, "${") || seen[img] {
			continue
		}
		seen[img] = true
		images = append(images, img)
	}
	sort.Strings(images)
	return images
}

// podmanStackImages returns the deduplicated, sorted list of image refs the
// new-user compose stack declares. `podman-compose config` (which resolves
// ${VAR:-default} substitutions properly) is tried first; a raw parse of
// the compose file is the fallback, for the same reasons install.sh itself
// falls back to a raw grep -- podman-compose isn't installed/working, or
// the file is present but podman-compose returned nothing usable.
func podmanStackImages() []string {
	dir := filepath.Dir(podmanStackComposePath)
	stdout, _, err := containerComposeCaptureRun("default", dir, "-f", podmanStackComposePath, "config")
	if err == nil {
		if images := podmanParseComposeImages([]byte(stdout)); len(images) > 0 {
			return podmanNormalizeAndDedupRefs(images)
		}
	}

	raw, err := os.ReadFile(podmanStackComposePath)
	if err != nil {
		return nil
	}
	return podmanNormalizeAndDedupRefs(podmanParseComposeImagesRaw(string(raw)))
}

// podmanImageUsage tallies, per image ID, how many containers reference it
// -- split into System (root's own local podman) and User (summed across
// every hosting user's separate rootless podman instance). Image storage
// is shared across all of these contexts on the host, so the same image ID
// can show up under both.
type podmanImageUsage struct {
	System int
	User   int
}

// podmanUserContexts returns the distinct non-local podman contexts --
// one per hosting user with their own rootless podman instance -- from the
// users table's `server` column (see the podman package doc comment: that
// column holds either a username, for a per-user rootless context, or one
// of the local values for root's own stack).
func podmanUserContexts(db *sql.DB) []string {
	if db == nil {
		return nil
	}
	rows, err := db.Query("SELECT DISTINCT server FROM users WHERE server IS NOT NULL AND server != ''")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var contexts []string
	for rows.Next() {
		var ctx string
		if err := rows.Scan(&ctx); err != nil {
			continue
		}
		if ctx != "" && !podman.IsLocal(ctx) {
			contexts = append(contexts, ctx)
		}
	}
	return contexts
}

// podmanContainerImageIDs runs `podman ps -a --format json` against context
// and returns the full ImageID of every container found there (running or
// stopped). A context that can't be reached (e.g. a user's rootless podman
// socket isn't up) is skipped rather than failing the whole tally -- one
// unreachable user shouldn't blank out usage counts for everyone else.
func podmanContainerImageIDs(context string) []string {
	out, err := podmanRunContextRun(context, "ps", "-a", "--format", "json")
	if err != nil {
		return nil
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil
	}
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		if id := podmanStringField(item, "ImageID", "Image ID"); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// podmanImageUsageCounts builds the full System/User usage tally: root's
// own containers first, then every hosting user's rootless containers
// concurrently (bounded, so a host with hundreds of users doesn't fire
// hundreds of podman processes at once).
func podmanImageUsageCounts(db *sql.DB) map[string]*podmanImageUsage {
	usage := map[string]*podmanImageUsage{}
	var mu sync.Mutex

	add := func(ids []string, system bool) {
		mu.Lock()
		defer mu.Unlock()
		for _, id := range ids {
			u, ok := usage[id]
			if !ok {
				u = &podmanImageUsage{}
				usage[id] = u
			}
			if system {
				u.System++
			} else {
				u.User++
			}
		}
	}

	add(podmanContainerImageIDs("default"), true)

	const maxConcurrency = 8
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for _, ctx := range podmanUserContexts(db) {
		wg.Add(1)
		sem <- struct{}{}
		go func(ctx string) {
			defer wg.Done()
			defer func() { <-sem }()
			add(podmanContainerImageIDs(ctx), false)
		}(ctx)
	}
	wg.Wait()

	return usage
}

// podmanListImages runs `podman images --format json` and maps it into
// display rows, annotated with usage from the given tally, then appends one
// synthetic NotDownloaded row for every stackImages ref that isn't already
// present (by repository:tag) among them -- so the table also surfaces
// stack images that are missing from the shared store entirely, not just
// ones that are present but unused.
func podmanListImages(usage map[string]*podmanImageUsage, stackImages []string) []podmanImageRow {
	out, err := podmanRunRun("images", "--format", "json")
	var raw []map[string]interface{}
	if err == nil {
		_ = json.Unmarshal([]byte(out), &raw)
	}

	updateStatus := podmanUpdateStatusSnapshot()
	vulnStatus := podmanVulnStatusSnapshot()

	rows := make([]podmanImageRow, 0, len(raw)+len(stackImages))
	present := map[string]bool{}
	seenIDs := map[string]bool{}
	for _, item := range raw {
		fullID := podmanStringField(item, "Id", "ID")
		// `additionalimagestores` in storage.conf (the shared-storage setup
		// every hosting user's rootless podman shares with root) makes
		// `podman images` list the exact same image ID twice: once as
		// root's own writable copy, once again as a read-only view through
		// the additional store, differing only by a "ReadOnly": true field.
		// Without this dedup every such image would render as two
		// identical rows in the table.
		if fullID != "" && seenIDs[fullID] {
			continue
		}
		if fullID != "" {
			seenIDs[fullID] = true
		}
		repo, tag := podmanImageRepoTag(item)
		if repo != "<none>" && tag != "<none>" {
			present[repo+":"+tag] = true
		}
		var system, user int
		if u := usage[fullID]; u != nil {
			system, user = u.System, u.User
		}
		var updateChecked, updateAvailable bool
		if st, ok := updateStatus[repo+":"+tag]; ok {
			updateChecked, updateAvailable = true, st.Available
		}
		var vulnChecked bool
		var vulnCount int
		if st, ok := vulnStatus[repo+":"+tag]; ok {
			vulnChecked, vulnCount = true, st.Count
		}
		sizeBytes, _ := item["Size"].(float64)
		rows = append(rows, podmanImageRow{
			ID:               podmanShortID(fullID),
			FullID:           fullID,
			Repository:       repo,
			Tag:              tag,
			Size:             podmanFormatSize(item["Size"]),
			SizeBytes:        sizeBytes,
			Created:          podmanFormatCreated(item["Created"]),
			SystemContainers: system,
			UserContainers:   user,
			UpdateChecked:    updateChecked,
			VulnChecked:      vulnChecked,
			VulnCount:        vulnCount,
			UpdateAvailable:  updateAvailable,
		})
	}

	for _, ref := range stackImages {
		repo, tag := podmanSplitRef(ref)
		if tag == "<none>" {
			// No tag in the compose file means podman would implicitly
			// pull/store it as ":latest" -- resolve that here too, both so
			// the presence check below actually matches an already-
			// downloaded image, and so the row (if still missing) shows
			// and pulls the tag podman will really use, not the literal
			// "<none>" placeholder.
			tag = "latest"
		}
		if present[repo+":"+tag] {
			continue
		}
		rows = append(rows, podmanImageRow{
			Repository:    repo,
			Tag:           tag,
			NotDownloaded: true,
		})
	}

	return rows
}

// podmanListVolumes runs `podman volume ls --format json` and maps it into
// display rows.
func podmanListVolumes() []podmanVolumeRow {
	out, err := podmanRunRun("volume", "ls", "--format", "json")
	if err != nil {
		return []podmanVolumeRow{}
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return []podmanVolumeRow{}
	}

	rows := make([]podmanVolumeRow, 0, len(raw))
	for _, item := range raw {
		rows = append(rows, podmanVolumeRow{
			Name:       podmanStringField(item, "Name"),
			Driver:     podmanStringField(item, "Driver"),
			Mountpoint: podmanStringField(item, "Mountpoint"),
			Created:    podmanFormatCreated(item["CreatedAt"]),
		})
	}
	return rows
}

// podmanListNetworks runs `podman network ls --format json` and maps it
// into display rows.
func podmanListNetworks() []podmanNetworkRow {
	out, err := podmanRunRun("network", "ls", "--format", "json")
	if err != nil {
		return []podmanNetworkRow{}
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return []podmanNetworkRow{}
	}

	rows := make([]podmanNetworkRow, 0, len(raw))
	for _, item := range raw {
		subnet := ""
		subnetsRaw, ok := item["Subnets"].([]interface{})
		if !ok {
			subnetsRaw, _ = item["subnets"].([]interface{})
		}
		if subnetsRaw != nil {
			var parts []string
			for _, s := range subnetsRaw {
				if sub, ok := s.(map[string]interface{}); ok {
					if v := podmanStringField(sub, "subnet", "Subnet"); v != "" {
						parts = append(parts, v)
					}
				}
			}
			subnet = strings.Join(parts, ", ")
		}
		created := item["Created"]
		if created == nil {
			created = item["created"]
		}
		rows = append(rows, podmanNetworkRow{
			ID:      podmanShortID(podmanStringField(item, "Id", "ID", "id")),
			Name:    podmanStringField(item, "Name", "name"),
			Driver:  podmanStringField(item, "Driver", "driver"),
			Subnet:  subnet,
			Created: podmanFormatCreated(created),
		})
	}
	return rows
}

type podmanDiskUsageRow struct {
	Type             string
	Total            int
	Active           int
	Size             string
	SizeBytes        float64 // raw value backing Size, for numeric sort
	Reclaimable      string
	ReclaimableBytes float64 // raw value backing Reclaimable, for numeric sort
}

// podmanSystemDiskUsage runs `podman system df --format json` (root/default
// context only) and maps it into display rows. The Images row genuinely
// reflects the WHOLE shared image store (every hosting user's images
// included, not just root's own) -- image content lives in one shared
// location, so there's only one store to measure -- but the
// Containers/Volumes rows are root's own local podman only, since
// containers and volumes (unlike images) are NOT shared across contexts.
// A run/parse failure yields an empty (not nil-panic) list.
func podmanSystemDiskUsage() []podmanDiskUsageRow {
	out, err := podmanRunRunStdout("system", "df", "--format", "json")
	if err != nil {
		return []podmanDiskUsageRow{}
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return []podmanDiskUsageRow{}
	}

	rows := make([]podmanDiskUsageRow, 0, len(raw))
	for _, item := range raw {
		total, _ := item["Total"].(float64)
		active, _ := item["Active"].(float64)
		sizeBytes, _ := item["RawSize"].(float64)
		reclaimableBytes, _ := item["RawReclaimable"].(float64)
		rows = append(rows, podmanDiskUsageRow{
			Type:             podmanStringField(item, "Type"),
			Total:            int(total),
			Active:           int(active),
			Size:             podmanStringField(item, "Size"),
			SizeBytes:        sizeBytes,
			Reclaimable:      podmanStringField(item, "Reclaimable"),
			ReclaimableBytes: reclaimableBytes,
		})
	}
	return rows
}

// ServePodman handles GET /server/podman.
func (p *Podman) ServePodman(w http.ResponseWriter, r *http.Request) {
	webtemplates.Render(w, "server_podman.html", mergeChrome(map[string]interface{}{
		"Info":      podmanInfoText(),
		"Images":    podmanListImages(podmanImageUsageCounts(p.MySQL), podmanStackImages()),
		"Volumes":   podmanListVolumes(),
		"Networks":  podmanListNetworks(),
		"DiskUsage": podmanSystemDiskUsage(),
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, p.Sessions),
	}, r, "Podman"))
}

// podmanSharedImageStoreRoot returns root's own configured
// "overlay.imagestore" path (from `podman info`) -- the actual, writable
// location image content lives in when podman's storage.conf splits image
// storage out from the container graphroot (see install.sh's SHARED_STORE:
// images are pulled with `podman --root "$SHARED_STORE" pull ...`). Some
// hosts' storage.conf also lists that same path under
// additionalimagestores, which podman always treats as a read-only view
// regardless of the underlying directory's real permissions -- so a plain
// `podman rmi <id>` (root's default context, no explicit --root) can
// resolve the ID to that read-only view and fail with "cannot remove
// read-only image" even for a genuinely unused image with a real writable
// copy. Returns "" if `podman info` doesn't report a split image store
// (nothing to route around).
func podmanSharedImageStoreRoot() string {
	out, err := podmanRunRun("info", "--format", "json")
	if err != nil {
		return ""
	}
	var info map[string]interface{}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return ""
	}
	store, _ := info["store"].(map[string]interface{})
	graphOptions, _ := store["graphOptions"].(map[string]interface{})
	imagestore, _ := graphOptions["overlay.imagestore"].(string)
	return imagestore
}

// podmanFixSharedStorePermissionsRun is injectable so tests never run real
// chmod/find/restorecon commands. It reapplies the exact fix-up
// install.sh's prefetch step performs on the shared image store right
// after it's first populated: podman (re)writes shared metadata files
// (notably overlay-layers/layers.json) with restrictive, owner-only
// permissions on every write it does -- including a routine pull or
// delete run from THIS page, not just the initial bulk prefetch. Without
// reapplying this after every such write, the very next pull/delete here
// silently breaks every hosting user's separate rootless podman from being
// able to read the shared store at all, surfacing to them as an opaque
// "permission denied" on their own site's next image pull.
var podmanFixSharedStorePermissionsRun = func(root string) {
	if root == "" {
		return
	}
	_ = exec.Command("chmod", "-R", "o+rX", root).Run()
	if out, err := exec.Command("find", root, "-name", "*.lock").Output(); err == nil {
		for _, lock := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if lock != "" {
				_ = exec.Command("chmod", "o+rw", lock).Run()
			}
		}
	}
	if _, err := exec.LookPath("restorecon"); err == nil {
		_ = exec.Command("restorecon", "-R", root).Run()
	}
}

// podmanDeleteImageRun / podmanPullImageRun are injectable so tests never
// shell out to the real podman binary.
//
// Delete targets the shared image store directly (via --root) when one is
// configured, precisely to route around the read-only-duplicate issue
// podmanSharedImageStoreRoot documents -- a plain `podman rmi` against
// root's default context is not used as the first attempt for that reason.
// But not every image is actually routed through the shared store (e.g.
// ones pulled before a shared-storage migration, or otherwise only present
// in root's own default context) -- for those, `--root <sharedstore> rmi`
// fails with "... : image not known" even though the image is real and
// genuinely deletable via a plain `podman rmi`. So a "not known" failure
// against the shared store falls back to a plain rmi against the default
// context before giving up.
//
// All attempts reapply podmanFixSharedStorePermissionsRun afterward, win or
// lose (a failed/partial pull can still have left new, badly-permissioned
// blob files behind) -- see that function's doc comment for why skipping
// this breaks every hosting user's own image pulls, not just root's.
var podmanDeleteImageRun = func(id string) (string, error) {
	root := podmanSharedImageStoreRoot()
	var out string
	var err error
	if root != "" {
		out, err = podmanRunRun("--root", root, "rmi", id)
		if err != nil && strings.Contains(out, "image not known") {
			out, err = podmanRunRun("rmi", id)
		}
	} else {
		out, err = podmanRunRun("rmi", id)
	}
	podmanFixSharedStorePermissionsRun(root)
	return out, err
}

var podmanPullImageRun = func(ref string) (string, error) {
	out, err := podmanRunRun("pull", ref)
	podmanFixSharedStorePermissionsRun(podmanSharedImageStoreRoot())
	return out, err
}

// podmanUpdateStatus is one repository:tag's last-checked update state.
type podmanUpdateStatus struct {
	Available bool
	CheckedAt time.Time
}

// podmanUpdateStatusCache holds the result of every "Check"/"Check all for
// updates" run so far, keyed by repository:tag. It's checked on demand
// only (never automatically -- see podmanCheckImageUpdateRun's doc
// comment), and lives only in memory: it resets on a service restart,
// which just means the next page load shows every image as not-yet-checked
// again, not stale/wrong data.
var (
	podmanUpdateStatusMu    sync.Mutex
	podmanUpdateStatusCache = map[string]podmanUpdateStatus{}
)

// podmanUpdateStatusSnapshot returns a point-in-time copy of the cache, so
// podmanListImages can read it without holding the lock across the whole
// images-table build.
func podmanUpdateStatusSnapshot() map[string]podmanUpdateStatus {
	podmanUpdateStatusMu.Lock()
	defer podmanUpdateStatusMu.Unlock()
	snap := make(map[string]podmanUpdateStatus, len(podmanUpdateStatusCache))
	for k, v := range podmanUpdateStatusCache {
		snap[k] = v
	}
	return snap
}

// podmanVulnDetail is one HIGH/CRITICAL finding from a Trivy scan.
type podmanVulnDetail struct {
	ID               string `json:"id"`                // e.g. CVE-2024-12345
	Package          string `json:"package"`
	InstalledVersion string `json:"installed_version"`
	FixedVersion     string `json:"fixed_version"`      // empty if no fix is published yet
	Severity         string `json:"severity"`
	Title            string `json:"title"`
	URL              string `json:"url"`
}

// podmanVulnStatus is one repository:tag's last-checked vulnerability scan
// result (HIGH+CRITICAL only).
type podmanVulnStatus struct {
	Count     int
	Details   []podmanVulnDetail
	CheckedAt time.Time
}

// podmanVulnStatusCache holds the result of every "Check all for
// vulnerabilities" run so far, keyed by repository:tag -- same on-demand,
// in-memory-only model as podmanUpdateStatusCache above.
var (
	podmanVulnStatusMu    sync.Mutex
	podmanVulnStatusCache = map[string]podmanVulnStatus{}
)

// podmanVulnStatusSnapshot returns a point-in-time copy of the cache, so
// podmanListImages can read it without holding the lock across the whole
// images-table build.
func podmanVulnStatusSnapshot() map[string]podmanVulnStatus {
	podmanVulnStatusMu.Lock()
	defer podmanVulnStatusMu.Unlock()
	snap := make(map[string]podmanVulnStatus, len(podmanVulnStatusCache))
	for k, v := range podmanVulnStatusCache {
		snap[k] = v
	}
	return snap
}

// PodmanTrivySocketPath is the root podman.socket's REST API socket that
// Trivy's --podman-host flag targets, so trivy scans the local podman
// store directly instead of trying (and failing) the Docker Engine it
// checks first by default. Injectable for tests.
var PodmanTrivySocketPath = "/run/podman/podman.sock"

// trivyInstalled reports whether the trivy binary is on PATH.
var trivyInstalled = func() bool {
	_, err := exec.LookPath("trivy")
	return err == nil
}

// trivyInstallRun runs Trivy's official install script, placing the
// binary at /usr/local/bin/trivy. Injectable for tests.
var trivyInstallRun = func() error {
	cmd := exec.Command("sh", "-c", "curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("trivy install failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// podmanEnsureTrivyRun installs Trivy on first use if it isn't already
// present -- there's no separate "install" step in the UI (see the Podman
// page's "Check all for vulnerabilities" button), so the first bulk check
// transparently installs it before scanning. Injectable for tests.
var podmanEnsureTrivyRun = func() error {
	if trivyInstalled() {
		return nil
	}
	return trivyInstallRun()
}

// podmanCheckImageVulnerabilitiesRun runs a Trivy vulnerability-only scan
// (HIGH/CRITICAL) against a locally-stored podman image and returns every
// finding. --image-src podman skips Trivy's default Docker-Engine-first
// lookup (which would otherwise waste time failing against a daemon that
// doesn't exist here) and --podman-host points it at root podman's own
// REST socket so it finds images already in the shared store without a
// registry round trip. Injectable so tests never shell out to a real
// trivy binary.
var podmanCheckImageVulnerabilitiesRun = func(ref string) (details []podmanVulnDetail, err error) {
	cmd := exec.Command("trivy", "image",
		"--scanners", "vuln",
		"--image-src", "podman",
		"--podman-host", PodmanTrivySocketPath,
		"--format", "json",
		"--severity", "HIGH,CRITICAL",
		"--quiet",
		ref,
	)
	// trivy sometimes writes progress/warning lines to stderr even on
	// success (e.g. DB update progress on first run); stdout alone is
	// parsed as JSON, mirroring podmanRunRunStdout's reasoning above.
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if runErr := cmd.Run(); runErr != nil {
		return nil, runErr
	}
	return podmanParseHighCriticalVulns(stdout.Bytes())
}

// podmanParseHighCriticalVulns extracts every HIGH/CRITICAL finding from a
// trivy `--format json` report. Split out from
// podmanCheckImageVulnerabilitiesRun so the parsing logic is unit-testable
// without a real trivy binary. The severity filter here is defensive/
// redundant with the CLI's own --severity flag, not load-bearing.
func podmanParseHighCriticalVulns(reportJSON []byte) ([]podmanVulnDetail, error) {
	var report struct {
		Results []struct {
			Vulnerabilities []struct {
				VulnerabilityID  string `json:"VulnerabilityID"`
				PkgName          string `json:"PkgName"`
				InstalledVersion string `json:"InstalledVersion"`
				FixedVersion     string `json:"FixedVersion"`
				Severity         string `json:"Severity"`
				Title            string `json:"Title"`
				PrimaryURL       string `json:"PrimaryURL"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		return nil, err
	}
	details := []podmanVulnDetail{}
	for _, result := range report.Results {
		for _, v := range result.Vulnerabilities {
			if v.Severity != "HIGH" && v.Severity != "CRITICAL" {
				continue
			}
			details = append(details, podmanVulnDetail{
				ID:               v.VulnerabilityID,
				Package:          v.PkgName,
				InstalledVersion: v.InstalledVersion,
				FixedVersion:     v.FixedVersion,
				Severity:         v.Severity,
				Title:            v.Title,
				URL:              v.PrimaryURL,
			})
		}
	}
	return details, nil
}

// podmanManifestDigestForPlatform picks out the digest of the sub-manifest
// matching os/arch from a `podman manifest inspect` response. Most
// registries wrap even a single-platform image in a manifest list/index
// these days, but for the rare case where manifestJSON is already a plain
// (non-list) manifest, its own digest is simply the sha256 of these exact
// bytes -- a manifest has no self-referential digest field, that's just
// what "digest" means per the OCI/Docker distribution spec.
func podmanManifestDigestForPlatform(manifestJSON []byte, os, arch string) (string, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(manifestJSON, &doc); err != nil {
		return "", err
	}
	manifests, ok := doc["manifests"].([]interface{})
	if !ok {
		sum := sha256.Sum256(manifestJSON)
		return "sha256:" + hex.EncodeToString(sum[:]), nil
	}
	for _, m := range manifests {
		entry, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		platform, _ := entry["platform"].(map[string]interface{})
		entryOS, _ := platform["os"].(string)
		entryArch, _ := platform["architecture"].(string)
		if entryOS == os && entryArch == arch {
			digest, _ := entry["digest"].(string)
			if digest == "" {
				return "", fmt.Errorf("manifest entry for %s/%s has no digest", os, arch)
			}
			return digest, nil
		}
	}
	return "", fmt.Errorf("no manifest entry for platform %s/%s", os, arch)
}

// podmanCheckImageUpdateRun is injectable so tests never hit a real
// registry. It compares the locally pulled image's manifest digest against
// the registry's current one for the same ref -- `podman image inspect` is
// local-only (no network), and `podman manifest inspect` fetches just the
// (small) manifest JSON, never the image content itself, so this is cheap
// per call but still a real network round trip -- which is exactly why
// it's only ever invoked on demand (a per-row "Check" click, or the bulk
// "Check all for updates" action) and never automatically on every page
// load, where doing this for every listed image would add many seconds.
var podmanCheckImageUpdateRun = func(ref string) (available bool, err error) {
	localOut, err := podmanRunRunStdout("image", "inspect", ref, "--format", "{{.Digest}}|{{.Os}}|{{.Architecture}}")
	if err != nil {
		return false, err
	}
	parts := strings.SplitN(strings.TrimSpace(localOut), "|", 3)
	if len(parts) != 3 || parts[0] == "" {
		return false, fmt.Errorf("could not determine local image digest for %s", ref)
	}
	localDigest, localOS, localArch := parts[0], parts[1], parts[2]

	remoteOut, err := podmanRunRunStdout("manifest", "inspect", ref)
	if err != nil {
		return false, err
	}
	remoteDigest, err := podmanManifestDigestForPlatform([]byte(remoteOut), localOS, localArch)
	if err != nil {
		return false, err
	}
	return remoteDigest != localDigest, nil
}

// podmanDeleteImageIfUnused re-checks usage right before actually deleting.
// podman itself only refuses a delete for a container it can see in the
// SAME (root/default) context -- it has no idea a hosting user's separate
// rootless podman instance has a container running against this same
// (shared-storage) image ID, so that has to be checked here first rather
// than relying on `podman rmi` alone to catch it.
func (p *Podman) podmanDeleteImageIfUnused(id string) (string, error) {
	if u := podmanImageUsageCounts(p.MySQL)[id]; u != nil && (u.System > 0 || u.User > 0) {
		return "", fmt.Errorf("image is still in use by %d system and %d user container(s)", u.System, u.User)
	}
	return podmanDeleteImageRun(id)
}

// pendingPodmanImageActions tracks in-flight async delete/pull actions
// fired from the images tab, keyed by image ID for delete or by ref
// (repository:tag) for pull -- same fire-a-goroutine-then-poll shape as
// pendingServiceActions in services.go, so a slow `podman pull` of a large
// image doesn't block the request (or any other tab sharing the
// connection) for its whole duration.
var (
	pendingPodmanImageActionsMu sync.Mutex
	pendingPodmanImageActions   = map[string]*podmanImageActionResult{}
)

type podmanImageActionResult struct {
	Done    bool   `json:"done"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ServePodmanImageAction handles POST
// /services/podman/images/{action}/{id...}: "delete" (id is the image's
// full ID) or "pull" (id is instead the repository:tag ref to pull -- this
// also covers a not-yet-downloaded stack image, which has no ID yet).
// Neither affects any already-running container, which keeps using
// whatever content it started with until it's recreated.
func (p *Podman) ServePodmanImageAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	id := r.PathValue("id")

	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "id is required")
		return
	}
	if action != "delete" && action != "pull" {
		writeJSONError(w, http.StatusBadRequest, "Invalid action. Use delete or pull.")
		return
	}

	result := &podmanImageActionResult{}
	pendingPodmanImageActionsMu.Lock()
	pendingPodmanImageActions[id] = result
	pendingPodmanImageActionsMu.Unlock()

	go func() {
		var out string
		var err error
		var successMsg, failMsg string
		if action == "delete" {
			out, err = p.podmanDeleteImageIfUnused(id)
			successMsg = "Image removed."
			failMsg = "Failed to remove image"
		} else {
			out, err = podmanPullImageRun(id)
			successMsg = "Pulled " + id + ". Running containers still use their original image content -- pull is done; now stop/remove and re-create (down/up) the containers using this image to actually apply the update."
			failMsg = "Failed to pull " + id
		}

		pendingPodmanImageActionsMu.Lock()
		result.Done = true
		if err != nil {
			msg := strings.TrimSpace(out)
			if msg == "" {
				msg = err.Error()
			}
			result.Success = false
			result.Message = failMsg + ": " + msg
		} else {
			result.Success = true
			result.Message = successMsg
		}
		pendingPodmanImageActionsMu.Unlock()
	}()

	writeJSON(w, map[string]bool{"scheduled": true})
}

// ServePodmanImageActionStatus handles GET
// /services/podman/images/action-status: the images tab polls this after
// firing an async delete/pull so it knows when to swap the "please wait"
// toast for the real outcome.
func (p *Podman) ServePodmanImageActionStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")

	pendingPodmanImageActionsMu.Lock()
	result, ok := pendingPodmanImageActions[id]
	var resp podmanImageActionResult
	if ok {
		resp = *result
	} else {
		resp = podmanImageActionResult{Done: true, Success: false, Message: "No action was recorded for this image. Please refresh and try again."}
	}
	pendingPodmanImageActionsMu.Unlock()

	writeJSON(w, resp)
}

// ServePodmanImageCheckUpdate handles GET
// /services/podman/images/check-update: a single, synchronous digest
// comparison against the registry (cheap -- one small manifest fetch, no
// blob download) for one image, so a per-row "Check" button doesn't need
// the schedule-then-poll dance the much slower/often-large pull and delete
// use. Result is cached in podmanUpdateStatusCache so the next page
// render (e.g. right after this call, when the caller reloads) reflects
// it without re-checking.
func (p *Podman) ServePodmanImageCheckUpdate(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		writeJSONError(w, http.StatusBadRequest, "ref is required")
		return
	}

	available, err := podmanCheckImageUpdateRun(ref)
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}

	podmanUpdateStatusMu.Lock()
	podmanUpdateStatusCache[ref] = podmanUpdateStatus{Available: available, CheckedAt: time.Now()}
	podmanUpdateStatusMu.Unlock()

	writeJSON(w, map[string]interface{}{"available": available})
}

// podmanBulkActionResult tracks progress for the Images tab's "Download
// all" / "Delete unused" buttons -- unlike pendingPodmanImageActions (keyed
// per image), only one bulk action makes sense at a time for the whole
// page, so this is a single shared pointer rather than a map.
type podmanBulkActionResult struct {
	Done      bool     `json:"done"`
	Total     int      `json:"total"`
	Completed int      `json:"completed"`
	Current   string   `json:"current"`
	Failed    []string `json:"failed"`
	Message   string   `json:"message"`
}

var (
	pendingPodmanBulkMu sync.Mutex
	pendingPodmanBulk   *podmanBulkActionResult
)

// ServePodmanImagesBulkAction handles POST
// /services/podman/images/bulk/{action}: "pull-missing" pulls every image
// the new-user compose stack references that isn't in the shared store
// yet; "delete-unused" removes every currently-downloaded image with zero
// system/user containers referencing it; "check-updates" runs
// podmanCheckImageUpdateRun against every currently-downloaded image and
// records the result in podmanUpdateStatusCache. All three run
// sequentially in the background (rather than firing a dozen podman
// processes, or registry requests, at once) while the browser polls
// ServePodmanImagesBulkStatus for progress.
func (p *Podman) ServePodmanImagesBulkAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "pull-missing" && action != "delete-unused" && action != "check-updates" && action != "check-vulnerabilities" {
		writeJSONError(w, http.StatusBadRequest, "Invalid action. Use pull-missing, delete-unused, check-updates, or check-vulnerabilities.")
		return
	}

	images := podmanListImages(podmanImageUsageCounts(p.MySQL), podmanStackImages())

	var refs []string
	switch action {
	case "pull-missing":
		for _, row := range images {
			if row.NotDownloaded {
				refs = append(refs, row.Repository+":"+row.Tag)
			}
		}
	case "delete-unused":
		for _, row := range images {
			if !row.NotDownloaded && row.SystemContainers == 0 && row.UserContainers == 0 {
				refs = append(refs, row.FullID)
			}
		}
	case "check-updates", "check-vulnerabilities":
		for _, row := range images {
			if !row.NotDownloaded && row.Repository != "<none>" && row.Tag != "<none>" {
				refs = append(refs, row.Repository+":"+row.Tag)
			}
		}
	}

	result := &podmanBulkActionResult{Total: len(refs)}
	pendingPodmanBulkMu.Lock()
	pendingPodmanBulk = result
	pendingPodmanBulkMu.Unlock()

	go func() {
		if action == "check-vulnerabilities" && len(refs) > 0 {
			pendingPodmanBulkMu.Lock()
			result.Current = "Installing Trivy…"
			pendingPodmanBulkMu.Unlock()
			if err := podmanEnsureTrivyRun(); err != nil {
				pendingPodmanBulkMu.Lock()
				result.Done = true
				result.Current = ""
				result.Message = "Failed to install Trivy: " + err.Error()
				pendingPodmanBulkMu.Unlock()
				return
			}
		}

		updatesAvailable := 0
		vulnerableImages := 0
		for _, ref := range refs {
			pendingPodmanBulkMu.Lock()
			result.Current = ref
			pendingPodmanBulkMu.Unlock()

			var err error
			switch action {
			case "pull-missing":
				_, err = podmanPullImageRun(ref)
			case "delete-unused":
				_, err = p.podmanDeleteImageIfUnused(ref)
			case "check-updates":
				var available bool
				available, err = podmanCheckImageUpdateRun(ref)
				if err == nil {
					podmanUpdateStatusMu.Lock()
					podmanUpdateStatusCache[ref] = podmanUpdateStatus{Available: available, CheckedAt: time.Now()}
					podmanUpdateStatusMu.Unlock()
					if available {
						updatesAvailable++
					}
				}
			case "check-vulnerabilities":
				var details []podmanVulnDetail
				details, err = podmanCheckImageVulnerabilitiesRun(ref)
				if err == nil {
					podmanVulnStatusMu.Lock()
					podmanVulnStatusCache[ref] = podmanVulnStatus{Count: len(details), Details: details, CheckedAt: time.Now()}
					podmanVulnStatusMu.Unlock()
					if len(details) > 0 {
						vulnerableImages++
					}
				}
			}

			pendingPodmanBulkMu.Lock()
			result.Completed++
			if err != nil {
				result.Failed = append(result.Failed, ref)
			}
			pendingPodmanBulkMu.Unlock()
		}

		pendingPodmanBulkMu.Lock()
		result.Done = true
		result.Current = ""
		succeeded := result.Total - len(result.Failed)
		switch action {
		case "pull-missing":
			result.Message = fmt.Sprintf("Pulled %d/%d images.", succeeded, result.Total)
		case "delete-unused":
			result.Message = fmt.Sprintf("Removed %d/%d unused images.", succeeded, result.Total)
		case "check-updates":
			result.Message = fmt.Sprintf("Checked %d/%d images -- %d update(s) available.", succeeded, result.Total, updatesAvailable)
		case "check-vulnerabilities":
			result.Message = fmt.Sprintf("Checked %d/%d images -- %d image(s) have known HIGH/CRITICAL vulnerabilities.", succeeded, result.Total, vulnerableImages)
		}
		pendingPodmanBulkMu.Unlock()
	}()

	writeJSON(w, map[string]interface{}{"scheduled": true, "total": len(refs)})
}

// ServePodmanImagesBulkStatus handles GET
// /services/podman/images/bulk-status.
func (p *Podman) ServePodmanImagesBulkStatus(w http.ResponseWriter, r *http.Request) {
	pendingPodmanBulkMu.Lock()
	defer pendingPodmanBulkMu.Unlock()
	if pendingPodmanBulk == nil {
		writeJSON(w, podmanBulkActionResult{Done: true, Message: "No bulk action has run yet."})
		return
	}
	writeJSON(w, *pendingPodmanBulk)
}

// ServePodmanImageVulnerabilities handles GET
// /services/podman/images/vulnerabilities: returns the cached Trivy
// findings for one image ref, populated by the last "Check all for
// vulnerabilities" run. Read-only lookup against podmanVulnStatusCache --
// this never triggers a scan itself.
func (p *Podman) ServePodmanImageVulnerabilities(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		writeJSONError(w, http.StatusBadRequest, "ref is required")
		return
	}

	podmanVulnStatusMu.Lock()
	st, ok := podmanVulnStatusCache[ref]
	podmanVulnStatusMu.Unlock()
	if !ok {
		writeJSON(w, map[string]interface{}{"checked": false, "details": []podmanVulnDetail{}})
		return
	}
	writeJSON(w, map[string]interface{}{"checked": true, "details": st.Details})
}
