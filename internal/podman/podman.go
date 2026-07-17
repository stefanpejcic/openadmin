// Package podman builds argv/env for podman commands against a "context"
// (a value from the users table's `server` column -- either a username for
// that user's rootless podman instance, or one of localContexts for root's
// own local stack).
package podman

import (
	"os"
	"os/exec"
	"os/user"
)

var localContexts = map[string]bool{"": true, "default": true, "root": true}

// IsLocal reports whether context refers to root's own local podman stack
// rather than a per-user remote one (an empty context maps to local too).
func IsLocal(context string) bool { return localContexts[context] }

// Socket returns the CONTAINER_HOST URL for a user's rootless podman
// instance.
func Socket(username string) (string, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return "", err
	}
	return "unix:///run/user/" + u.Uid + "/podman/podman.sock", nil
}

// Env is the local process environment, plus CONTAINER_HOST for a per-user
// (non-local) context. extra is layered on top last, so it always wins.
func Env(context string, extra map[string]string) ([]string, error) {
	env := os.Environ()
	if !IsLocal(context) {
		sock, err := Socket(context)
		if err != nil {
			return nil, err
		}
		env = append(env, "CONTAINER_HOST="+sock)
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env, nil
}

// Argv builds "podman" for a local context, "podman --remote" otherwise.
func Argv(context string, args ...string) []string {
	prefix := []string{"podman"}
	if !IsLocal(context) {
		prefix = []string{"podman", "--remote"}
	}
	return append(prefix, args...)
}

// Command builds an *exec.Cmd for a podman invocation against context,
// with the env already wired up. The caller sets Dir/Stdout/Stderr/etc. as
// needed before Run()/Start().
func Command(context string, args ...string) (*exec.Cmd, error) {
	argv := Argv(context, args...)
	env, err := Env(context, nil)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	return cmd, nil
}

// ComposeCommand builds an *exec.Cmd for podman-compose. No --remote flag
// is passed (podman-compose inserts extra podman args after the
// subcommand, where --remote isn't valid; CONTAINER_HOST alone is enough
// for it to auto-detect remote mode).
func ComposeCommand(context string, args ...string) (*exec.Cmd, error) {
	env, err := Env(context, nil)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("podman-compose", args...)
	cmd.Env = env
	return cmd, nil
}
