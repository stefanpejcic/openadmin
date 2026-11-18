package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
)

// Root's own store and every user's rootless store list the shared image store under
// additionalimagestores, so their images and containers sit on top of layers that physically
// live in the shared store. `podman --root <shared> rmi` can't see any of that and happily
// deletes those layers, which corrupts every image built on them (caddy on alpine, etc).

var errPodmanSharedLayerInUse = errors.New("its layers are still used by images or containers in another store")

type podmanStorageLayer struct {
	ID     string `json:"id"`
	Parent string `json:"parent"`
}

type podmanStorageImage struct {
	ID              string   `json:"id"`
	Layer           string   `json:"layer"`
	MappedTopLayers []string `json:"mapped-top-layers"`
}

// podmanReadStoreJSON reads overlay-<kind>/<kind>.json from a store, a missing file just means an empty store.
func podmanReadStoreJSON(root, kind string, v interface{}) (bool, error) {
	data, err := os.ReadFile(filepath.Join(root, "overlay-"+kind, kind+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(data, v)
}

// podmanExternalLayerRefs collects every layer ID the given stores build on, from their layers' parents and their images' top layers.
func podmanExternalLayerRefs(roots []string) (map[string]bool, error) {
	refs := map[string]bool{}
	for _, root := range roots {
		var layers []podmanStorageLayer
		if _, err := podmanReadStoreJSON(root, "layers", &layers); err != nil {
			return nil, fmt.Errorf("reading layers of %s: %w", root, err)
		}
		for _, l := range layers {
			if l.Parent != "" {
				refs[l.Parent] = true
			}
		}
		var images []podmanStorageImage
		if _, err := podmanReadStoreJSON(root, "images", &images); err != nil {
			return nil, fmt.Errorf("reading images of %s: %w", root, err)
		}
		for _, im := range images {
			if im.Layer != "" {
				refs[im.Layer] = true
			}
			for _, m := range im.MappedTopLayers {
				refs[m] = true
			}
		}
	}
	return refs, nil
}

// podmanSharedLayersRemovedBy mirrors what `rmi` does in the shared store: walk down from the top layer, stopping at the first layer another shared image or layer still uses.
func podmanSharedLayersRemovedBy(sharedRoot, id string) ([]string, error) {
	var images []podmanStorageImage
	if _, err := podmanReadStoreJSON(sharedRoot, "images", &images); err != nil {
		return nil, err
	}
	var layers []podmanStorageLayer
	if _, err := podmanReadStoreJSON(sharedRoot, "layers", &layers); err != nil {
		return nil, err
	}

	var top string
	found := false
	usedTop := map[string]bool{}
	for _, im := range images {
		if !found && (im.ID == id || (len(id) >= 12 && strings.HasPrefix(im.ID, id))) {
			top, found = im.Layer, true
			continue
		}
		usedTop[im.Layer] = true
		for _, m := range im.MappedTopLayers {
			usedTop[m] = true
		}
	}
	if !found {
		return nil, nil
	}

	parent := make(map[string]string, len(layers))
	children := map[string]int{}
	for _, l := range layers {
		parent[l.ID] = l.Parent
		if l.Parent != "" {
			children[l.Parent]++
		}
	}
	var removed []string
	for cur := top; cur != "" && !usedTop[cur] && children[cur] == 0; {
		removed = append(removed, cur)
		p, ok := parent[cur]
		if !ok {
			break
		}
		if p != "" {
			children[p]--
		}
		cur = p
	}
	return removed, nil
}

var podmanGraphrootRe = regexp.MustCompile(`(?m)^\s*graphroot\s*=\s*"([^"]+)"`)

// podmanUserGraphroot reads graphroot from a user's storage.conf, falling back to rootless podman's default.
func podmanUserGraphroot(home string) string {
	if data, err := os.ReadFile(filepath.Join(home, ".config", "containers", "storage.conf")); err == nil {
		if m := podmanGraphrootRe.FindSubmatch(data); m != nil {
			return string(m[1])
		}
	}
	return filepath.Join(home, ".local", "share", "containers", "storage")
}

// podmanOtherStoreRoots lists root's graphroot plus every local user's store, including users OpenPanel forgot about but whose files are still on disk.
func podmanOtherStoreRoots(db *sql.DB, sharedRoot string) []string {
	seen := map[string]bool{}
	var roots []string
	add := func(root string) {
		root = filepath.Clean(root)
		if root == "" || root == "." || seen[root] || root == filepath.Clean(sharedRoot) {
			return
		}
		seen[root] = true
		roots = append(roots, root)
	}

	if out, err := podmanRunRunStdout("info", "--format", "json"); err == nil {
		var info struct {
			Store struct {
				GraphRoot string `json:"graphRoot"`
			} `json:"store"`
		}
		if json.Unmarshal([]byte(out), &info) == nil && info.Store.GraphRoot != "" {
			add(info.Store.GraphRoot)
		}
	}
	add("/var/lib/containers/storage")

	homes := map[string]bool{}
	if db != nil {
		if rows, err := db.Query("SELECT username FROM users"); err == nil {
			for rows.Next() {
				var name string
				if rows.Scan(&name) == nil && name != "" {
					if u, err := user.Lookup(name); err == nil && u.HomeDir != "" {
						homes[u.HomeDir] = true
					}
				}
			}
			rows.Close()
		}
	}
	if matches, err := filepath.Glob("/home/*/.config/containers/storage.conf"); err == nil {
		for _, m := range matches {
			homes[filepath.Dir(filepath.Dir(filepath.Dir(m)))] = true
		}
	}
	for home := range homes {
		add(podmanUserGraphroot(home))
	}
	return roots
}

// podmanSharedLayerGuardRun refuses a shared-store delete that would pull layers out from under another store; injectable for tests.
var podmanSharedLayerGuardRun = func(db *sql.DB, id string) error {
	shared := podmanSharedImageStoreRoot()
	if shared == "" {
		return nil
	}
	return podmanCheckSharedLayers(shared, podmanOtherStoreRoots(db, shared), id)
}

func podmanCheckSharedLayers(sharedRoot string, otherRoots []string, id string) error {
	removed, err := podmanSharedLayersRemovedBy(sharedRoot, id)
	if err != nil {
		return fmt.Errorf("can't read the shared image store, not deleting: %w", err)
	}
	if len(removed) == 0 {
		return nil
	}
	refs, err := podmanExternalLayerRefs(otherRoots)
	if err != nil {
		return fmt.Errorf("can't check other image stores, not deleting: %w", err)
	}
	for _, l := range removed {
		if refs[l] {
			return errPodmanSharedLayerInUse
		}
	}
	return nil
}
