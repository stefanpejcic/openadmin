package handlers

import "testing"

func TestPodmanFormatSize(t *testing.T) {
	cases := map[float64]string{
		0:          "0 B",
		153000000:  "153 MB",
		279200000:  "279.2 MB",
		6960000000: "6.96 GB",
		6964000000: "6.96 GB",
	}
	for in, want := range cases {
		if got := podmanFormatSize(in); got != want {
			t.Errorf("podmanFormatSize(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestPodmanFormatReclaimable(t *testing.T) {
	if got := podmanFormatReclaimable(4110000000, 6960000000); got != "4.11 GB (59%)" {
		t.Errorf("got %q", got)
	}
	if got := podmanFormatReclaimable(0, 100); got != "0 B" {
		t.Errorf("got %q", got)
	}
}

func TestPodmanInfoSummary(t *testing.T) {
	raw := `host:
  cgroupVersion: v2
  distribution:
    distribution: almalinux
    version: "10.0"
  kernel: 6.12.0
store:
  containerStore:
    number: 3
  graphDriverName: overlay
  imageStore:
    number: 12
version:
  Version: 5.4.0
`
	got := podmanInfoSummary(raw)
	want := []podmanInfoField{
		{"Podman version", "5.4.0"},
		{"OS / kernel", "almalinux 10.0 / 6.12.0"},
		{"Storage driver", "overlay"},
		{"Cgroup version", "v2"},
		{"Containers", "3"},
		{"Images", "12"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d fields, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if podmanInfoSummary("Failed to get podman info: boom") != nil {
		t.Error("expected nil for non-yaml input")
	}
}
