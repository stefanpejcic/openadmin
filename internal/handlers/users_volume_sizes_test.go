package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserVolumeSizesUsesDuTotals(t *testing.T) {
	home := t.TempDir()
	orig, origRun := UserHomeDir, volumeSizesRun
	UserHomeDir = home
	t.Cleanup(func() { UserHomeDir, volumeSizesRun = orig, origRun })
	dir := filepath.Join(home, "bob", "docker-data", "volumes", "bob_html_data", "_data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	volumeSizesRun = func(dirs []string) (string, error) { return "13000\t" + dir + "\n", nil }
	sizes := userVolumeSizes("bob")
	if sizes["bob_html_data"] != 13000 {
		t.Fatalf("got %v", sizes)
	}
	line := withOwnVolumeSize(`{"Type":"Local Volumes","Size":"62.32GB"}`, "bob")
	if line != `{"Size":"13kB","Type":"Local Volumes"}` {
		t.Fatalf("got %s", line)
	}
}
