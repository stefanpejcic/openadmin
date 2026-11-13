package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
)

// injectable so tests don't shell out to podman
var userPodmanRun = func(context string, args ...string) (stdout, stderr string, exitCode int, err error) {
	return podmanExecCapture(context, args...)
}

// same output as podman's own df, decimal units with 4 significant digits
func podmanHumanSize(n int64) string {
	units := []string{"B", "kB", "MB", "GB", "TB", "PB"}
	f := float64(n)
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	return strconv.FormatFloat(f, 'g', 4, 64) + units[i]
}

// parses podman sizes like "110.1MB", "8.71kB" or "0B" back to bytes
func parsePodmanSize(s string) int64 {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " "); i > 0 {
		s = s[:i]
	}
	mult := map[string]float64{"B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "PB": 1e15}
	j := len(s)
	for j > 0 && (s[j-1] < '0' || s[j-1] > '9') {
		j--
	}
	num, err := strconv.ParseFloat(s[:j], 64)
	if err != nil {
		return 0
	}
	m, ok := mult[strings.ToUpper(s[j:])]
	if !ok {
		return 0
	}
	return int64(num * m)
}

// userOwnImagesRow builds the Images df row from only the user's own images, the shared store ones are ReadOnly and owned by root
func userOwnImagesRow(context string) (map[string]interface{}, bool) {
	out, _, code, err := userPodmanRun(context, "images", "--format", "json")
	if err != nil || code != 0 {
		return nil, false
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	var total, active int
	var size, reclaimable int64
	for _, img := range raw {
		if ro, _ := img["ReadOnly"].(bool); ro {
			continue
		}
		id, _ := img["Id"].(string)
		if id != "" && seen[id] {
			continue
		}
		seen[id] = true
		sz, _ := img["Size"].(float64)
		containers, _ := img["Containers"].(float64)
		total++
		size += int64(sz)
		if containers > 0 {
			active++
		} else {
			reclaimable += int64(sz)
		}
	}
	pct := 0
	if size > 0 {
		pct = int(float64(reclaimable) / float64(size) * 100)
	}
	return map[string]interface{}{
		"Type":           "Images",
		"Total":          total,
		"Active":         active,
		"RawSize":        size,
		"RawReclaimable": reclaimable,
		"TotalCount":     total,
		"Size":           podmanHumanSize(size),
		"Reclaimable":    fmt.Sprintf("%s (%d%%)", podmanHumanSize(reclaimable), pct),
	}, true
}

type volumeMount struct {
	Container   string `json:"container"`
	State       string `json:"state"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

type userVolume struct {
	Name        string            `json:"name"`
	Driver      string            `json:"driver"`
	Scope       string            `json:"scope"`
	Mountpoint  string            `json:"mountpoint"`
	CreatedAt   string            `json:"created_at"`
	Description string            `json:"description"`
	Purpose     string            `json:"purpose"`
	Project     string            `json:"project"`
	Labels      map[string]string `json:"labels"`
	Options     map[string]string `json:"options"`
	Size        int64             `json:"size"`
	SizeHuman   string            `json:"size_human"`
	SizeKnown   bool              `json:"size_known"`
	Links       int               `json:"links"`
	UsedBy      []volumeMount     `json:"used_by"`
}

// volume sizes only come from the text output, podman refuses --format with -v
func parseDfVolumeSizes(out string) map[string][2]int64 {
	res := map[string][2]int64{}
	in := false
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "Local Volumes space usage") {
			in = true
			continue
		}
		if !in || t == "" || strings.HasPrefix(t, "VOLUME NAME") {
			continue
		}
		if strings.HasSuffix(t, "space usage:") {
			break
		}
		f := strings.Fields(t)
		if len(f) < 3 {
			continue
		}
		links, _ := strconv.ParseInt(f[1], 10, 64)
		res[f[0]] = [2]int64{links, parsePodmanSize(f[2])}
	}
	return res
}

func userVolumeMounts(context string) map[string][]volumeMount {
	res := map[string][]volumeMount{}
	ids, _, code, err := userPodmanRun(context, "ps", "-aq")
	if err != nil || code != 0 {
		return res
	}
	idList := strings.Fields(ids)
	if len(idList) == 0 {
		return res
	}
	out, _, code, err := userPodmanRun(context, append([]string{"container", "inspect"}, idList...)...)
	if err != nil || code != 0 {
		return res
	}
	var containers []struct {
		Name   string
		State  struct{ Status string }
		Mounts []struct {
			Type        string
			Name        string
			Destination string
			RW          bool
		}
	}
	if err := json.Unmarshal([]byte(out), &containers); err != nil {
		return res
	}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type != "volume" || m.Name == "" {
				continue
			}
			res[m.Name] = append(res[m.Name], volumeMount{Container: c.Name, State: c.State.Status, Destination: m.Destination, RW: m.RW})
		}
	}
	return res
}

// ServeUserVolumes handles GET /client/volumes/{username}
func (u *Users) ServeUserVolumes(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	currentUser := auth.CurrentUser(r)
	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	context, _ := queryContextByUsername(u.MySQL, username)
	out, stderr, code, err := userPodmanRun(context, "volume", "ls", "--format", "json")
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if code != 0 {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("podman volume ls exited with status %d: %s", code, strings.TrimSpace(stderr)),
		})
		return
	}
	var raw []struct {
		Name       string
		Driver     string
		Scope      string
		Mountpoint string
		CreatedAt  string
		Labels     map[string]string
		Options    map[string]string
	}
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &raw); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "invalid JSON in podman output"})
			return
		}
	}

	sizes := map[string][2]int64{}
	if dfOut, _, dfCode, dfErr := userPodmanRun(context, "system", "df", "-v"); dfErr == nil && dfCode == 0 {
		sizes = parseDfVolumeSizes(dfOut)
	}
	mounts := userVolumeMounts(context)

	vols := make([]userVolume, 0, len(raw))
	for _, v := range raw {
		labels := v.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		project := labels["com.docker.compose.project"]
		if project == "" {
			project = labels["io.podman.compose.project"]
		}
		vol := userVolume{
			Name: v.Name, Driver: v.Driver, Scope: v.Scope, Mountpoint: v.Mountpoint, CreatedAt: v.CreatedAt,
			Description: labels["description"], Purpose: labels["purpose"], Project: project,
			Labels: labels, Options: v.Options, UsedBy: mounts[v.Name],
		}
		if vol.UsedBy == nil {
			vol.UsedBy = []volumeMount{}
		}
		if s, ok := sizes[v.Name]; ok {
			vol.Links, vol.Size, vol.SizeKnown = int(s[0]), s[1], true
			vol.SizeHuman = podmanHumanSize(s[1])
		}
		vols = append(vols, vol)
	}
	sort.Slice(vols, func(i, j int) bool {
		if vols[i].Size != vols[j].Size {
			return vols[i].Size > vols[j].Size
		}
		return vols[i].Name < vols[j].Name
	})
	writeJSON(w, vols)
}
