package handlers

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestTasksDefault(t *testing.T) {
	for in, want := range map[string]int{"6g": 6000, "2G": 2000, "1": 1000} {
		if got, _ := tasksDefault(in); got != want {
			t.Errorf("%s: got %d want %d", in, got, want)
		}
	}
}

// a fake home and slice with the test process's uid, the way readUserSlice finds them
func fakeSlice(t *testing.T, files map[string]string) string {
	t.Helper()
	homes, slices := t.TempDir(), t.TempDir()
	origHome, origSlice, origSample := containerHomeBase, userSliceRoot, sliceCPUSample
	t.Cleanup(func() { containerHomeBase, userSliceRoot, sliceCPUSample = origHome, origSlice, origSample })
	containerHomeBase, userSliceRoot, sliceCPUSample = homes, slices, time.Millisecond
	os.MkdirAll(filepath.Join(homes, "alice"), 0755)
	if files != nil {
		dir := filepath.Join(slices, "user-"+strconv.Itoa(os.Getuid())+".slice")
		os.MkdirAll(dir, 0755)
		for name, v := range files {
			os.WriteFile(filepath.Join(dir, name), []byte(v), 0644)
		}
	}
	return homes
}

func TestReadUserSliceAndTasks(t *testing.T) {
	homes := fakeSlice(t, map[string]string{
		"memory.current": "1073741824\n", "memory.max": "6442450944\n", "cpu.max": "400000 100000\n",
		"pids.current": "300\n", "pids.max": "6000\n", "cpu.stat": "usage_usec 1000\n",
	})
	live, ok := readUserSlice("alice")
	if !ok || live.MemMax != 6442450944 || live.CPUCores != 4 || live.PidsCur != 300 || live.PidsMax != 6000 {
		t.Fatalf("live %+v ok %v", live, ok)
	}
	w := detailStatsWidget{}.withLive(live)
	if w.RAMHumanTotal != "6.0G" || w.CPUHumanTotal != "4 cores" || w.RAMPercentRounded != "16.67" {
		t.Fatalf("widget %+v", w)
	}
	tasks := readUserTasks("alice", "6g", &live, nil)
	if !tasks.Known || tasks.Pct != "5" || tasks.MaxHuman != "6000" || tasks.Default != 6000 || tasks.Override != 0 {
		t.Fatalf("tasks %+v", tasks)
	}
	os.WriteFile(filepath.Join(homes, "alice", "TasksMax"), []byte("9000\n"), 0644)
	if got := readUserTasks("alice", "6g", &live, nil); got.Override != 9000 {
		t.Fatalf("override %+v", got)
	}
}

func TestReadUserTasksFallsBackToUsageFile(t *testing.T) {
	fakeSlice(t, nil)
	if _, ok := readUserSlice("alice"); ok {
		t.Fatal("no slice should mean no live data")
	}
	stats := map[string]interface{}{"tasks": map[string]interface{}{"current": 50.0, "limit": 1000.0}}
	got := readUserTasks("alice", "1g", nil, stats)
	if !got.Known || got.Current != 50 || got.Max != 1000 || got.Pct != "5" {
		t.Fatalf("got %+v", got)
	}
	if got := readUserTasks("alice", "1g", nil, nil); got.Known {
		t.Fatalf("nothing to read should stay unknown, got %+v", got)
	}
}
