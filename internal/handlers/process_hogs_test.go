package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindProcessHogs(t *testing.T) {
	dir := t.TempDir()
	users := filepath.Join(dir, "users")
	os.MkdirAll(filepath.Join(users, "bob"), 0755)
	os.MkdirAll(filepath.Join(users, "ana"), 0755)
	subuid := filepath.Join(dir, "subuid")
	os.WriteFile(subuid, []byte("ubuntu:100000:65536\nbob:165536:65536\n"), 0644)

	procs := []processInfo{
		{PID: 1, Name: "mariadbd", Owner: "170000", CPUPercent: 95},
		{PID: 2, Name: "php-fpm", Owner: "ana", MemoryPercent: 25},
		{PID: 3, Name: "caddy", Owner: "root", CPUPercent: 120},
		{PID: 4, Name: "node", Owner: "120000", CPUPercent: 99},
		{PID: 5, Name: "sshd", Owner: "root", CPUPercent: 5},
	}
	got := findProcessHogs(procs, users, subuid)
	if len(got) != 4 {
		t.Fatalf("expected 4 hogs, got %+v", got)
	}
	want := map[int]string{1: "bob", 2: "ana", 3: "", 4: ""}
	for _, h := range got {
		if h.PanelUser != want[h.PID] {
			t.Fatalf("PID %d PanelUser = %q, want %q", h.PID, h.PanelUser, want[h.PID])
		}
	}
}
