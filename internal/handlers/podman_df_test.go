package handlers

import "testing"

func TestPodmanParseDiskUsageJSON(t *testing.T) {
	// podman 4.4+: one array, formatted strings plus raw numbers
	v49 := `[{"Type":"Images","Total":3,"Active":2,"RawSize":1000,"RawReclaimable":250,"Size":"1kB","Reclaimable":"250B (25%)"},{"Type":"Containers","Total":2,"Active":2,"RawSize":0,"RawReclaimable":0,"Size":"0B","Reclaimable":"0B (0%)"}]`
	// podman 4.3 (Debian 12): an object per line, Size/Reclaimable are the raw numbers
	v43 := "{\"Type\":\"Images\",\"Total\":3,\"Active\":2,\"Size\":1000,\"Reclaimable\":250}\n{\"Type\":\"Containers\",\"Total\":2,\"Active\":2,\"Size\":0,\"Reclaimable\":0}\n{\"Type\":\"Local Volumes\",\"Total\":1,\"Active\":1,\"Size\":4096,\"Reclaimable\":0}\n"
	for name, out := range map[string]string{"4.9": v49, "4.3": v43} {
		raw := podmanParseDiskUsageJSON(out)
		if len(raw) < 2 || raw[0]["Type"] != "Images" {
			t.Fatalf("%s: got %v", name, raw)
		}
	}
	if podmanParseDiskUsageJSON("Error: template: x") != nil {
		t.Fatal("non-json output should give nil")
	}
}
