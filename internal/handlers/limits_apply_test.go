package handlers

import (
	"os"
	"slices"
	"testing"
)

const limitsTestCompose = `services:
  openpanel_redis:
    image: redis
    container_name: openpanel_redis
    deploy:
      resources:
        limits:
          cpus: "${REDIS_CPUS:-0.5}"
          memory: "${REDIS_RAM:-0.5G}"
  caddy:
    container_name: caddy
    mem_limit: ${CADDY_RAM}
    cpus: ${CADDY_CPUS}
  nolimits:
    image: busybox
    cpus: ${ORPHAN_CPUS}
`

func TestLimitTargets(t *testing.T) {
	got := limitTargets(limitsTestCompose)
	want := map[string]limitTarget{
		"REDIS_CPUS": {"openpanel_redis", "cpus"}, "REDIS_RAM": {"openpanel_redis", "memory"},
		"CADDY_RAM": {"caddy", "memory"}, "CADDY_CPUS": {"caddy", "cpus"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %v, want %v", k, got[k], v)
		}
	}
}

func TestApplyLimitsLive(t *testing.T) {
	dir := t.TempDir()
	origCompose, origRun := LimitsComposePath, limitsPodmanUpdateRun
	t.Cleanup(func() { LimitsComposePath, limitsPodmanUpdateRun = origCompose, origRun })
	LimitsComposePath = dir + "/docker-compose.yml"
	os.WriteFile(LimitsComposePath, []byte(limitsTestCompose), 0644)
	var calls []string
	limitsPodmanUpdateRun = func(container, flag string) error {
		calls = append(calls, container+" "+flag)
		return nil
	}
	applied, failed := applyLimitsLive(map[string]string{"REDIS_RAM": "2G", "CADDY_CPUS": "1.5", "UNKNOWN_CPUS": "1"})
	slices.Sort(calls)
	slices.Sort(applied)
	if !slices.Equal(calls, []string{"caddy --cpus=1.5", "openpanel_redis --memory=2g"}) || !slices.Equal(applied, []string{"caddy", "openpanel_redis"}) || len(failed) != 0 {
		t.Fatalf("calls %v applied %v failed %v", calls, applied, failed)
	}
}
