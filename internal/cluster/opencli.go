package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// opencliTimeout is long enough for opencli update or user-add on a slow server
const opencliTimeout = 30 * time.Minute

// opencliAllowed are the only opencli commands the master can run on a slave
var opencliAllowed = map[string]bool{
	// listings
	"user-list": true, "domains-all": true, "domains-user": true, "domains-whoowns": true,
	"websites-all": true, "websites-user": true, "plan-list": true, "ftp-list": true,
	"email-manage": true,
	// account and domain actions, run where the account lives
	"user-suspend": true, "user-unsuspend": true, "user-password": true, "user-change_plan": true,
	"user-quota": true, "user-2fa": true, "user-email": true, "user-login": true, "user-delete": true,
	"user-add": true, "domains-add": true, "domains-suspend": true, "domains-unsuspend": true,
	"domains-delete": true, "domains-ssl": true, "domains-dns": true, "domains-docroot": true,
	// settings and server commands with --all-servers
	"config": true, "php-default": true, "waf": true, "plan-create": true, "plan-edit": true,
	"plan-delete": true, "domains-update_ns": true, "license": true, "update": true,
	"sentinel": true, "version": true, "imunify": true, "api": true, "email-server": true,
}

type opencliResult struct {
	Server string `json:"server"`
	RC     int    `json:"rc"`
	Out    string `json:"out"`
	Error  string `json:"error,omitempty"`
}

// runOpencli is the slave side of cluster-exec, args go straight to opencli without a shell
func runOpencli(raw json.RawMessage) (opencliResult, error) {
	var args []string
	if err := json.Unmarshal(raw, &args); err != nil || len(args) == 0 {
		return opencliResult{}, errors.New("invalid opencli call")
	}
	if !opencliAllowed[args[0]] {
		return opencliResult{}, fmt.Errorf("opencli %s can't be run from the master", args[0])
	}
	// email-manage hands its args to podman exec, so only the account list goes through
	if args[0] == "email-manage" && strings.Join(args, " ") != "email-manage setup email list" {
		return opencliResult{}, errors.New("only opencli email-manage setup email list can be run from the master")
	}
	for _, a := range args {
		if strings.ContainsRune(a, 0) {
			return opencliResult{}, errors.New("invalid opencli argument")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), opencliTimeout)
	defer cancel()
	// the slave's opencli must not try to fan out again
	cmd := exec.CommandContext(ctx, "/usr/local/bin/opencli", args...)
	cmd.Env = append(os.Environ(), "OPENCLI_CLUSTER_LOCAL=1")
	if run, err := exec.LookPath("systemd-run"); err == nil {
		// its own unit, so api, license and update can restart this service without killing the command
		cmd = exec.CommandContext(ctx, run, append([]string{"--quiet", "--collect", "--wait", "--pipe",
			"--setenv=OPENCLI_CLUSTER_LOCAL=1", "--", "/usr/local/bin/opencli"}, args...)...)
	}
	out, err := cmd.CombinedOutput()
	res := opencliResult{Out: string(out)}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		res.RC = exit.ExitCode()
	case err != nil:
		return res, err
	}
	return res, nil
}

// hasAccount also finds a suspended account, those are renamed to SUSPENDED_<time>_<name>
func (m *Manager) hasAccount(name string) (bool, error) {
	var n int
	err := m.MySQL.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ? OR username LIKE ?`,
		name, `SUSPENDED\_%\_`+strings.ReplaceAll(name, "_", `\_`)).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return n > 0, err
}

// CLILicensed tells shell commands whether the license is valid, main sets it
var CLILicensed func() bool

// cliManager is the cluster as seen from a shell on this server, with room for long opencli runs
func cliManager() *Manager {
	host, _ := os.Hostname()
	return &Manager{Hostname: host, Licensed: CLILicensed, rpcTimeout: opencliTimeout + time.Minute}
}

// Servers lists the live slaves for opencli, it's empty when this isn't a master with slaves
func Servers() []string {
	m := cliManager()
	if !m.IsMaster() {
		return nil
	}
	var names []string
	for _, n := range m.LiveNodes() {
		names = append(names, n.Name)
	}
	return names
}

// pickNodes is every live slave for "all", or the one named, matched by name, short name or id
func (m *Manager) pickNodes(target string) ([]Node, error) {
	nodes := m.LiveNodes()
	if target == "" || target == "all" {
		return nodes, nil
	}
	for _, n := range m.State().Nodes {
		short, _, _ := strings.Cut(n.Name, ".")
		if strings.EqualFold(n.Name, target) || strings.EqualFold(short, target) || n.ID == target {
			if n.Paused {
				return nil, fmt.Errorf("%s is paused", n.Name)
			}
			return []Node{n}, nil
		}
	}
	return nil, fmt.Errorf("no server named %s in the cluster", target)
}

// Exec runs opencli args on the target slaves at once and returns their results in cluster order
func Exec(target string, args []string) ([]opencliResult, error) {
	m := cliManager()
	if !m.IsMaster() {
		return nil, errors.New("this server is not a cluster master")
	}
	nodes, err := m.pickNodes(target)
	if err != nil {
		return nil, err
	}
	results := make([]opencliResult, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func(i int, n Node) {
			defer wg.Done()
			res := opencliResult{}
			if err := m.rpc(n, "opencli", args, &res); err != nil {
				res = opencliResult{RC: 1, Error: err.Error()}
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "connection reset") {
					res.Error = "OpenAdmin restarted while the command ran, it keeps running there, check the result with --server=" + n.Name
				}
			}
			res.Server = n.Name
			results[i] = res
		}(i, n)
	}
	wg.Wait()
	return results, nil
}

// Find names the slaves that have the account (kind "user") or the domain (kind "domain")
func Find(kind, value string) ([]string, error) {
	m := cliManager()
	if !m.IsMaster() {
		return nil, errors.New("this server is not a cluster master")
	}
	if kind == "user" && !accountRe.MatchString(value) {
		return nil, errors.New("invalid account")
	}
	nodes := m.LiveNodes()
	found := make([]bool, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func(i int, n Node) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
			defer cancel()
			if kind == "user" {
				var ok bool
				found[i] = m.rpcContext(ctx, n, "has_account", accountArgs{Account: value}, &ok) == nil && ok
				return
			}
			var taken []string
			found[i] = m.rpcContext(ctx, n, "domains_taken", []string{value}, &taken) == nil && len(taken) > 0
		}(i, n)
	}
	wg.Wait()
	var names []string
	for i, n := range nodes {
		if found[i] {
			names = append(names, n.Name)
		}
	}
	return names, nil
}
