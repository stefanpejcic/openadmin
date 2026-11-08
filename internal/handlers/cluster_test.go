package handlers

import (
	"testing"

	"openadmin/internal/cluster"
)

func TestClusterProblems(t *testing.T) {
	n := clusterProblems([]cluster.NodeHealth{
		{Name: "stefan", Online: true, DiskPct: 40},
		{Name: "lazar", Online: false, Error: "invalid login"},
		{Name: "petar", Online: true, DiskPct: 95},
		{Name: "radovan", Paused: true},
	})
	if !n.Down || len(n.Lines) != 2 || n.Lines[0] != "lazar: offline (invalid login)." || n.Lines[1] != "petar: disk 95% full." {
		t.Fatalf("got %+v", n)
	}
	if n := clusterProblems([]cluster.NodeHealth{{Name: "petar", Online: true, MemPct: 92}}); n.Down || len(n.Lines) != 1 {
		t.Fatalf("memory only should be a warning: %+v", n)
	}
}
