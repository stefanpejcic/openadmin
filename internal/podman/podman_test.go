package podman

import (
	"os"
	"os/user"
	"strings"
	"testing"
)

func TestIsLocal(t *testing.T) {
	cases := map[string]bool{
		"":        true,
		"default": true,
		"root":    true,
		"alice":   false,
		"bob123":  false,
	}
	for context, want := range cases {
		if got := IsLocal(context); got != want {
			t.Errorf("IsLocal(%q) = %v, want %v", context, got, want)
		}
	}
}

func TestArgvLocalContext(t *testing.T) {
	got := Argv("default", "ps", "-a")
	want := []string{"podman", "ps", "-a"}
	if !equalSlices(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestArgvRemoteContext(t *testing.T) {
	got := Argv("alice", "ps", "-a")
	want := []string{"podman", "--remote", "ps", "-a"}
	if !equalSlices(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEnvLocalContextHasNoContainerHost(t *testing.T) {
	env, err := Env("default", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "CONTAINER_HOST=") {
			t.Fatalf("expected no CONTAINER_HOST for a local context, got %v", env)
		}
	}
}

func TestEnvRemoteContextSetsContainerHost(t *testing.T) {
	// use the current OS user so user.Lookup succeeds in the test sandbox
	me, err := user.Current()
	if err != nil {
		t.Skip("no current user available in this environment")
	}

	env, err := Env(me.Username, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "CONTAINER_HOST=unix:///run/user/"+me.Uid+"/podman/podman.sock") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected CONTAINER_HOST pointing at %s's rootless socket, got %v", me.Username, env)
	}
}

func TestEnvUnknownUserErrors(t *testing.T) {
	if _, err := Env("definitely-not-a-real-user-12345", nil); err == nil {
		t.Fatal("expected an error looking up a nonexistent user")
	}
}

func TestEnvExtraOverridesBase(t *testing.T) {
	os.Setenv("PODMAN_TEST_MARKER", "original")
	defer os.Unsetenv("PODMAN_TEST_MARKER")

	env, err := Env("default", map[string]string{"PODMAN_TEST_MARKER": "overridden"})
	if err != nil {
		t.Fatal(err)
	}
	var last string
	for _, kv := range env {
		if strings.HasPrefix(kv, "PODMAN_TEST_MARKER=") {
			last = kv
		}
	}
	if last != "PODMAN_TEST_MARKER=overridden" {
		t.Fatalf("expected extra env to override the base value, got %q", last)
	}
}

func TestCommandBuildsExpectedArgvAndEnv(t *testing.T) {
	cmd, err := Command("default", "version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cmd.Path, "podman") && cmd.Args[0] != "podman" {
		t.Fatalf("expected the podman binary to be invoked, got %v", cmd.Args)
	}
	if len(cmd.Args) < 2 || cmd.Args[1] != "version" {
		t.Fatalf("expected 'version' arg, got %v", cmd.Args)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
