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

func TestPodmanInfoOverview(t *testing.T) {
	raw := `host:
  arch: amd64
  cgroupManager: systemd
  cgroupVersion: v2
  conmon:
    version: 'conmon version 2.1.13, commit: unknown'
  cpus: 8
  distribution:
    distribution: almalinux
    version: "10.0"
  kernel: 6.12.0
  memFree: 2147483648
  memTotal: 8589934592
  ociRuntime:
    name: crun
    version: |-
      crun version 1.21
      commit: abc
  uptime: 1732h 43m 5.00s (Approximately 72.17 days)
store:
  containerStore:
    number: 3
    running: 2
    stopped: 1
  graphDriverName: overlay
  graphRootAllocated: 107374182400
  graphRootUsed: 53687091200
  graphStatus:
    Backing Filesystem: extfs
  imageStore:
    number: 12
version:
  Version: 5.4.0
`
	got := podmanInfoOverviewFrom(raw)
	if got == nil {
		t.Fatal("expected an overview")
	}
	if got.Version != "5.4.0" || got.Runtime != "crun 1.21" || got.Running != 2 || got.Stopped != 1 || got.Images != 12 || got.StoragePct != 50 {
		t.Fatalf("got %+v", got)
	}
	fields := map[string]string{}
	for _, sec := range got.Sections {
		for _, f := range sec.Fields {
			fields[f.Label] = f.Value
		}
	}
	if fields["Uptime"] != "72.17 days" || fields["Conmon"] != "2.1.13" || fields["Driver"] != "overlay on extfs" || fields["OS"] != "almalinux 10.0" {
		t.Fatalf("fields %v", fields)
	}
	if podmanInfoOverviewFrom("Failed to get podman info: boom") != nil {
		t.Error("expected nil for non-yaml input")
	}
}
