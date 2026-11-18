package handlers

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeStore(t *testing.T, root string, layers []podmanStorageLayer, images []podmanStorageImage) {
	t.Helper()
	for kind, v := range map[string]interface{}{"layers": layers, "images": images} {
		dir := filepath.Join(root, "overlay-"+kind)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(dir, kind+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPodmanCheckSharedLayers(t *testing.T) {
	tmp := t.TempDir()
	shared := filepath.Join(tmp, "shared")
	root := filepath.Join(tmp, "root")
	user := filepath.Join(tmp, "user")

	// shared: alpine (a1), debian base (d1) with two php images on it (p1, p2), and an orphan image (o1)
	writeStore(t, shared, []podmanStorageLayer{
		{ID: "a1"},
		{ID: "d1"}, {ID: "p1", Parent: "d1"}, {ID: "p2", Parent: "d1"},
		{ID: "o1"},
		{ID: "u1"},
	}, []podmanStorageImage{
		{ID: "alpine0000000000", Layer: "a1"},
		{ID: "php83000000000000", Layer: "p1"},
		{ID: "php84000000000000", Layer: "p2"},
		{ID: "orphan00000000000", Layer: "o1"},
		{ID: "userbase000000000", Layer: "u1"},
	})
	// root store: caddy built on alpine, mailserver on the debian base
	writeStore(t, root, []podmanStorageLayer{
		{ID: "c1", Parent: "a1"}, {ID: "m1", Parent: "d1"},
	}, []podmanStorageImage{
		{ID: "caddy000000000000", Layer: "c1"},
		{ID: "mail0000000000000", Layer: "m1"},
	})
	// a user's container layer sitting right on a shared image
	writeStore(t, user, []podmanStorageLayer{{ID: "ctr", Parent: "u1"}}, nil)

	others := []string{root, user, filepath.Join(tmp, "missing")}

	cases := []struct {
		id      string
		blocked bool
	}{
		{"alpine0000000000", true},   // would delete a1 that caddy needs
		{"userbase000000000", true},  // user's container layer needs u1
		{"php83000000000000", false}, // d1 survives, p2 still uses it
		{"orphan00000000000", false}, // nothing elsewhere builds on it
		{"notinshared000000", false}, // not a shared image, plain rmi handles it
		{"alpine000000", true},       // short IDs match too
	}
	for _, c := range cases {
		err := podmanCheckSharedLayers(shared, others, c.id)
		if c.blocked != errors.Is(err, errPodmanSharedLayerInUse) {
			t.Errorf("%s: blocked=%v, got err %v", c.id, c.blocked, err)
		}
	}

	// once the only other php image is gone, deleting php83 would take d1 with it, which mailserver needs
	writeStore(t, shared, []podmanStorageLayer{{ID: "d1"}, {ID: "p1", Parent: "d1"}},
		[]podmanStorageImage{{ID: "php83000000000000", Layer: "p1"}})
	if err := podmanCheckSharedLayers(shared, others, "php83000000000000"); !errors.Is(err, errPodmanSharedLayerInUse) {
		t.Errorf("last image on a base layer used elsewhere should be blocked, got %v", err)
	}

	// a corrupt store elsewhere means we can't tell, so refuse
	os.WriteFile(filepath.Join(user, "overlay-layers", "layers.json"), []byte("{"), 0o644)
	if err := podmanCheckSharedLayers(shared, others, "php83000000000000"); err == nil {
		t.Error("unreadable store should block the delete")
	}
}
