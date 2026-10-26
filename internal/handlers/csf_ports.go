package handlers

import (
	"os"
	"strconv"
	"strings"
)

var CSFConfPath = "/etc/csf/csf.conf"

// csfTCPPortOpen reports whether port is reachable through CSF's TCP_IN, with no CSF config there's no firewall so it's open
func csfTCPPortOpen(port, confPath string) bool {
	p, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(confPath)
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(key) != "TCP_IN" {
			continue
		}
		for _, item := range strings.Split(strings.Trim(strings.TrimSpace(val), `"`), ",") {
			lo, hi, isRange := strings.Cut(strings.TrimSpace(item), ":")
			if !isRange {
				hi = lo
			}
			l, err1 := strconv.Atoi(lo)
			h, err2 := strconv.Atoi(hi)
			if err1 == nil && err2 == nil && p >= l && p <= h {
				return true
			}
		}
		return false
	}
	return false
}
