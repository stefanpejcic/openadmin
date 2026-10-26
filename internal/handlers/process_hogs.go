package handlers

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var SubuidPath = "/etc/subuid"

const (
	hogCPUPercent = 90
	hogMemPercent = 20
)

type processHog struct {
	processInfo
	// PanelUser is set when the process belongs to an OpenPanel user's container
	PanelUser string
}

type subuidRange struct {
	user       string
	start, end int
}

func readSubuidRanges(path string) []subuidRange {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []subuidRange
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) != 3 {
			continue
		}
		start, err1 := strconv.Atoi(parts[1])
		count, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, subuidRange{parts[0], start, start + count})
	}
	return out
}

// panelUserFor finds the OpenPanel user owning a process: their own username, or a raw uid inside their rootless subuid range
func panelUserFor(owner, usersDir string, ranges []subuidRange) string {
	isPanelUser := func(name string) bool {
		info, err := os.Stat(filepath.Join(usersDir, name))
		return name != "" && err == nil && info.IsDir()
	}
	if uid, err := strconv.Atoi(owner); err == nil {
		for _, r := range ranges {
			if uid >= r.start && uid < r.end && isPanelUser(r.user) {
				return r.user
			}
		}
		return ""
	}
	if isPanelUser(owner) {
		return owner
	}
	return ""
}

func findProcessHogs(procs []processInfo, usersDir, subuidPath string) []processHog {
	var ranges []subuidRange
	var out []processHog
	for _, p := range procs {
		if p.CPUPercent < hogCPUPercent && p.MemoryPercent < hogMemPercent {
			continue
		}
		if ranges == nil {
			ranges = readSubuidRanges(subuidPath)
		}
		out = append(out, processHog{processInfo: p, PanelUser: panelUserFor(p.Owner, usersDir, ranges)})
	}
	return out
}
