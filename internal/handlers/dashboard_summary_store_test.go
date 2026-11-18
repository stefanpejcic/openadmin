package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoredDailySummary(t *testing.T) {
	dir := t.TempDir()
	orig, origCM := DailySummaryDir, ClusterManager
	t.Cleanup(func() { DailySummaryDir, ClusterManager = orig, origCM })
	DailySummaryDir, ClusterManager = dir, nil

	// a file older than the retention goes away when a new day is saved
	old := time.Now().AddDate(0, 0, -40).Format("2006-01-02")
	os.WriteFile(filepath.Join(dir, old+".json"), []byte(`{"date":"`+old+`"}`), 0o640)

	path, err := GenerateDailySummary(nil, "2099-01-02", "s1")
	if err != nil || filepath.Base(path) != "2099-01-02.json" {
		t.Fatalf("generate: %q %v", path, err)
	}
	if _, err := GenerateDailySummary(nil, "18.11.2026", "s1"); err == nil {
		t.Error("a bad date should be refused")
	}
	if _, err := os.Stat(filepath.Join(dir, old+".json")); !os.IsNotExist(err) {
		t.Error("the 40 day old summary should be pruned")
	}
	st := loadStoredDailySummary("", 1)
	if st == nil || st.Date != "2099-01-02" {
		t.Fatalf("newest saved day: %+v", st)
	}
	if loadStoredDailySummary("2099-01-03", 1) != nil {
		t.Error("a day that wasn't saved should be nil")
	}
	if got := storedDailySummaryDates(); len(got) != 1 || got[0] != "2099-01-02" {
		t.Errorf("dates: %v", got)
	}
}
