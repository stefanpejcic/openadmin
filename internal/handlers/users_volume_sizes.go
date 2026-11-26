package handlers

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// volumeDirsFor maps each of the user's volume names to its _data dir
func volumeDirsFor(userContext string) map[string]string {
	root := filepath.Join(UserHomeDir, userContext, "docker-data", "volumes")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name(), "_data")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			out[e.Name()] = dir
		}
	}
	return out
}

// podman df walks into nfs and other mounts inside a volume, du -x stays on the volume's own disk like the quota does
var volumeSizesRun = func(dirs []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// every volume of the account in one go, kept off the sites' disk time
	name, args := lowPrio(true, "du", append([]string{"-sx", "-B1", "--"}, dirs...)...)
	out, err := exec.CommandContext(ctx, name, args...).Output()
	// du exits 1 on an unreadable file but still prints the totals
	if len(out) > 0 {
		return string(out), nil
	}
	return "", err
}

// userVolumeSizes is bytes per volume name, nil when du couldn't run
func userVolumeSizes(userContext string) map[string]int64 {
	dirs := volumeDirsFor(userContext)
	if len(dirs) == 0 {
		return nil
	}
	byDir := map[string]string{}
	list := make([]string, 0, len(dirs))
	for name, dir := range dirs {
		byDir[dir] = name
		list = append(list, dir)
	}
	out, err := volumeSizesRun(list)
	if err != nil {
		return nil
	}
	sizes := map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		size, dir, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
		if name, known := byDir[strings.TrimSpace(dir)]; known && err == nil {
			sizes[name] = n
		}
	}
	if len(sizes) == 0 {
		return nil
	}
	return sizes
}

// withOwnVolumeSize swaps the df Local Volumes size for the du total
func withOwnVolumeSize(line, userContext string) string {
	sizes := userVolumeSizes(userContext)
	if sizes == nil {
		return line
	}
	var row map[string]interface{}
	if json.Unmarshal([]byte(line), &row) != nil {
		return line
	}
	var total int64
	for _, n := range sizes {
		total += n
	}
	row["Size"] = podmanHumanSize(total)
	b, err := json.Marshal(row)
	if err != nil {
		return line
	}
	return string(b)
}
