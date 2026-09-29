package handlers

import (
	"testing"
	"time"

	"openadmin/internal/paneldb"
)

func TestUsersSortKeysTwoFA(t *testing.T) {
	rows := []paneldb.RowMap{
		{"username": "a", "twofa_enabled": int64(0)},
		{"username": "b", "twofa_enabled": int64(1)},
	}
	sortRowMaps(rows, usersSortKeys["twofa"], true)
	if rows[0]["username"] != "b" {
		t.Fatalf("expected 2FA users first when sorting desc, got %v", rows)
	}
}

func TestSortRowMapsTimeAndFloat(t *testing.T) {
	now := time.Now()
	rows := []paneldb.RowMap{
		{"username": "new", "registered_date": now, "cpu_usage": 1.5},
		{"username": "old", "registered_date": now.Add(-48 * time.Hour), "cpu_usage": 80.0},
	}
	sortRowMaps(rows, usersSortKeys["date"], false)
	if rows[0]["username"] != "old" {
		t.Fatalf("expected oldest first ascending, got %v", rows)
	}
	sortRowMaps(rows, "cpu_usage", true)
	if rows[0]["username"] != "old" {
		t.Fatalf("expected highest cpu first descending, got %v", rows)
	}
}

func TestAnnotateUserUsage(t *testing.T) {
	row := paneldb.RowMap{}
	annotateUserUsage(row, usageWidgetData{MemPercent: 40, CPUPercent: 12})
	if row["memory_usage"] != int64(40) || row["cpu_usage"] != int64(12) {
		t.Fatalf("unexpected usage: %v", row)
	}
	if _, ok := row["disk_usage"]; ok {
		t.Fatalf("no disk data should leave disk unset so it sorts apart: %v", row)
	}
	annotateUserUsage(row, usageWidgetData{HasDiskData: true, DiskPercent: 70, InodesPercent: 5})
	if row["disk_usage"] != int64(70) || row["inodes_usage"] != int64(5) {
		t.Fatalf("unexpected disk usage: %v", row)
	}
}
