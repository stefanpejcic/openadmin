package handlers

import (
	"os/exec"
	"slices"
	"testing"
)

func TestLowPrio(t *testing.T) {
	name, args := lowPrio(true, "du", "-sx", "/home/x")
	all := append([]string{name}, args...)
	if !slices.Equal(all[len(all)-3:], []string{"du", "-sx", "/home/x"}) {
		t.Fatalf("command not kept at the end: %v", all)
	}
	if _, err := exec.LookPath("ionice"); err == nil && !slices.Contains(all, "-c3") {
		t.Fatalf("idle class missing: %v", all)
	}
	if _, args := lowPrio(false, "du"); slices.Contains(args, "-c3") {
		t.Fatalf("ionice without idle: %v", args)
	}
}
