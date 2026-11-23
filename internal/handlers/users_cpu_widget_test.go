package handlers

import (
	"encoding/json"
	"testing"
)

// panel's real sample: 56% of a 2 core limit, the cli's own human says 0.6 cores
const panelCPUSample = `{"cpu":{"usage":{"pct":56,"human":"0.6 cores"},"total":{"pct":200,"human":"2.0 cores"}},"memory":{"total":{"bytes":4294967296,"human":"4.0G"},"used":{"human":"2.5G"},"usage_pct":63}}`

func TestDetailWidgetCPUIsShareOfLimit(t *testing.T) {
	var stats map[string]interface{}
	if err := json.Unmarshal([]byte(panelCPUSample), &stats); err != nil {
		t.Fatal(err)
	}
	w := buildDetailStatsWidget(false, diskUsage{InodesUsed: 50, InodesHard: 100, DiskUsed: 10, DiskHard: 1000}, stats)
	if w.CPURatioRounded != "56" || w.CPUHumanUsage != "1.1 cores" || w.CPUHumanTotal != "2.0 cores" {
		t.Fatalf("got %s%% %s / %s", w.CPURatioRounded, w.CPUHumanUsage, w.CPUHumanTotal)
	}
	if w.InodesUsageRounded != "50" {
		t.Fatalf("inodes should be against the inodes limit, got %s", w.InodesUsageRounded)
	}
	if u := buildUsageWidget(false, stats, diskUsage{}, false); u.CPUPercent != 56 {
		t.Fatalf("list widget cpu got %d", u.CPUPercent)
	}
}
