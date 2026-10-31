package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

const (
	JobRunning   = "running"
	JobCopied    = "copied"
	JobFailed    = "failed"
	JobFinalized = "finalized"
	JobCancelled = "cancelled"

	transferPoll = 5 * time.Second
	transferMax  = 12 * time.Hour
)

// Job is one account move, kept in cluster.json so it survives the admin restart a transfer causes on the destination
type Job struct {
	ID         string    `json:"id"`
	Account    string    `json:"account"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	FromName   string    `json:"from_name"`
	ToName     string    `json:"to_name"`
	Live       bool      `json:"live"`
	Owner      string    `json:"owner,omitempty"`
	Reseller   string    `json:"reseller,omitempty"`
	Status     string    `json:"status"`
	Step       string    `json:"step"`
	Error      string    `json:"error,omitempty"`
	Log        string    `json:"log,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// call runs action on a server, the master talks to itself without the network
func (m *Manager) call(id, action string, data, out any) error {
	return m.callContext(context.Background(), id, action, data, out)
}

func (m *Manager) callContext(ctx context.Context, id, action string, data, out any) error {
	if id == LocalNode {
		var raw json.RawMessage
		if data != nil {
			b, err := json.Marshal(data)
			if err != nil {
				return err
			}
			raw = b
		}
		res, err := m.handleAction(action, raw)
		if err != nil || out == nil {
			return err
		}
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	n, ok := m.liveNodeByID(id)
	if !ok {
		return errors.New("unknown server")
	}
	return m.rpcContext(ctx, n, action, data, out)
}

func (m *Manager) serverName(id string) string {
	if id == LocalNode {
		return m.displayName()
	}
	if n, ok := m.nodeByID(id); ok {
		return n.Name
	}
	return id
}

// serverIP is a real ip on purpose, user-transfer writes this value into dns zones
func (m *Manager) serverIP(id string) (string, error) {
	if id == LocalNode {
		if net.ParseIP(m.PublicIP) == nil {
			return "", errors.New("could not determine this server's ip")
		}
		return m.PublicIP, nil
	}
	n, ok := m.nodeByID(id)
	if !ok {
		return "", errors.New("unknown server")
	}
	host := hostOnly(n.URL)
	if net.ParseIP(host) != nil {
		return host, nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("%s has no ipv4 address", host)
}

// Accounts lists the movable accounts per server
func (m *Manager) Accounts() map[string][]string {
	out := map[string][]string{}
	ids := []string{LocalNode}
	for _, n := range m.LiveNodes() {
		ids = append(ids, n.ID)
	}
	for _, id := range ids {
		var list []string
		if m.call(id, "accounts", nil, &list) == nil {
			out[id] = list
		}
	}
	return out
}

// preflight refuses the cases the transfer can't carry over correctly, before anything is touched
func (m *Manager) preflight(account, from, to, reseller string) (string, error) {
	if from == to {
		return "", errors.New("source and destination are the same server")
	}
	if !accountRe.MatchString(account) {
		return "", errors.New("invalid account name")
	}
	var src accountInfo
	if err := m.call(from, "account_info", accountArgs{Account: account}, &src); err != nil {
		return "", err
	}
	if !src.Exists {
		return "", fmt.Errorf("account %s does not exist on %s", account, m.serverName(from))
	}
	owner := ""
	if src.Owner != "" && src.Owner != "root" {
		owner = src.Owner
		if reseller != ResellerCopy && reseller != ResellerDrop {
			return "", fmt.Errorf("account %s belongs to reseller %s, choose to copy the reseller or leave the account without one", account, owner)
		}
		if reseller == ResellerCopy {
			var role string
			if err := m.call(to, "admin_role", owner, &role); err != nil {
				return "", err
			}
			if role != "" && role != "reseller" {
				return "", fmt.Errorf("%s is already an admin on %s, leave the account without a reseller or rename that admin", owner, m.serverName(to))
			}
		}
	}
	var dst accountInfo
	if err := m.call(to, "account_info", accountArgs{Account: account}, &dst); err != nil {
		return "", err
	}
	if dst.Exists {
		return "", fmt.Errorf("username %s is already taken on %s", account, m.serverName(to))
	}
	var taken []string
	if err := m.call(to, "domains_taken", src.Domains, &taken); err != nil {
		return "", err
	}
	if len(taken) > 0 {
		return "", fmt.Errorf("domains already on %s: %s", m.serverName(to), strings.Join(taken, ", "))
	}
	return owner, nil
}

// StartMove validates and kicks off an account move in the background
func (m *Manager) StartMove(account, from, to string, live bool, reseller string) (*Job, error) {
	if !m.IsMaster() {
		return nil, errors.New("multi-server requires a valid Enterprise license")
	}
	owner, err := m.preflight(account, from, to, reseller)
	if err != nil {
		return nil, err
	}
	for _, j := range m.State().Jobs {
		if j.Account == account && (j.Status == JobRunning || j.Status == JobCopied) {
			return nil, fmt.Errorf("account %s already has an unfinished move", account)
		}
	}
	job := &Job{
		ID: randomHex(8), Account: account, From: from, To: to, FromName: m.serverName(from), ToName: m.serverName(to),
		Live: live, Owner: owner, Reseller: reseller, Status: JobRunning, Step: "preparing", StartedAt: time.Now(),
	}
	if err := m.update(func(s *State) error {
		s.Jobs = append(s.Jobs, job)
		if len(s.Jobs) > 50 {
			s.Jobs = s.Jobs[len(s.Jobs)-50:]
		}
		return nil
	}); err != nil {
		return nil, err
	}
	go m.runMove(*job)
	Notify("Account move started", job.Account+" is being moved from "+job.FromName+" to "+job.ToName+".", "info")
	return job, nil
}

func (m *Manager) saveJob(id string, fn func(*Job)) {
	_ = m.update(func(s *State) error {
		for _, j := range s.Jobs {
			if j.ID == id {
				fn(j)
			}
		}
		return nil
	})
}

func (m *Manager) failMove(job Job, cause error) {
	Notify("Account move failed", job.Account+" from "+job.FromName+" to "+job.ToName+": "+cause.Error(), "warning")
	m.saveJob(job.ID, func(j *Job) {
		j.Status, j.Step, j.Error, j.FinishedAt = JobFailed, "failed", cause.Error(), time.Now()
	})
}

func (m *Manager) runMove(job Job) {
	fromIP, err := m.serverIP(job.From)
	if err != nil {
		m.failMove(job, err)
		return
	}
	destIP, err := m.serverIP(job.To)
	if err != nil {
		m.failMove(job, err)
		return
	}
	m.saveJob(job.ID, func(j *Job) { j.Step = "authorizing ssh" })
	var pub string
	if err := m.call(job.From, "ssh_key", nil, &pub); err != nil {
		m.failMove(job, err)
		return
	}
	if err := m.call(job.To, "ssh_authorize", sshAuthorizeArgs{Pub: pub, From: fromIP, Tag: job.ID}, nil); err != nil {
		m.failMove(job, err)
		return
	}
	m.saveJob(job.ID, func(j *Job) { j.Step = "copying account" })
	args := accountArgs{Account: job.Account, Job: job.ID, DestIP: destIP, Live: job.Live}
	// a server without an ed25519 host key stays on the old unverified connection
	_ = m.call(job.To, "ssh_hostkey", nil, &args.HostKey)
	if err := m.call(job.From, "transfer_start", args, nil); err != nil {
		_ = m.call(job.To, "ssh_revoke", sshAuthorizeArgs{Tag: job.ID}, nil)
		m.failMove(job, err)
		return
	}
	m.watchMove(job)
}

// watchMove waits for the detached transfer, it also picks jobs back up after a restart
func (m *Manager) watchMove(job Job) {
	deadline := job.StartedAt.Add(transferMax)
	for time.Now().Before(deadline) {
		time.Sleep(transferPoll)
		var st transferState
		if err := m.call(job.From, "transfer_state", accountArgs{Job: job.ID}, &st); err != nil {
			continue // source or its admin restarting, keep waiting
		}
		m.saveJob(job.ID, func(j *Job) { j.Log = st.Log })
		if !st.Done {
			continue
		}
		m.revokeWithRetry(job)
		if st.RC != 0 {
			// the name wasn't on the destination when we started, so whatever is there now is ours
			rollbackErr := m.retryCall(job.To, "account_delete", accountArgs{Account: job.Account})
			msg := fmt.Sprintf("user-transfer exited with code %d", st.RC)
			if rollbackErr != nil {
				msg += ", could not remove the partial copy: " + rollbackErr.Error()
			} else {
				msg += ", partial copy removed from " + job.ToName
			}
			m.failMove(job, errors.New(msg))
			return
		}
		resellerErr := m.finishReseller(job)
		// a dedicated ip belongs to the old server, the copy goes on the new server's shared ip
		ipErr := m.retryCall(job.To, "account_shared_ip", accountArgs{Account: job.Account})
		m.saveJob(job.ID, func(j *Job) {
			j.Status, j.Step, j.FinishedAt = JobCopied, "copied, waiting for you to finish", time.Now()
			Notify("Account copied", job.Account+" is on "+job.ToName+" now, delete it from "+job.FromName+" once DNS has moved, or cancel the move.", "info")
			if resellerErr != nil {
				j.Error = "the account moved but its reseller wasn't handled: " + resellerErr.Error()
			}
			if ipErr != nil {
				j.Error = strings.TrimPrefix(j.Error+"; ", "; ") + "the account moved but couldn't be switched to the shared ip: " + ipErr.Error()
			}
		})
		return
	}
	m.revokeWithRetry(job)
	m.failMove(job, errors.New("transfer timed out"))
}

// destination restarts its admin at the end of a transfer, so its first answers can fail
func (m *Manager) retryCall(id, action string, data any) error {
	var err error
	for i := 0; i < 12; i++ {
		if err = m.call(id, action, data, nil); err == nil {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return err
}

func (m *Manager) revokeWithRetry(job Job) {
	if err := m.retryCall(job.To, "ssh_revoke", sshAuthorizeArgs{Tag: job.ID}); err != nil {
		m.saveJob(job.ID, func(j *Job) { j.Error = "ssh key still authorized on " + job.ToName + ": " + err.Error() })
	}
}

// ResumeMoves is called once at startup
func (m *Manager) ResumeMoves() {
	for _, j := range m.State().Jobs {
		if j.Status == JobRunning && !j.StartedAt.IsZero() {
			go m.watchMove(*j)
		}
	}
}

func (m *Manager) finishMove(id string, deleteFromSource bool) error {
	var job *Job
	for _, j := range m.State().Jobs {
		if j.ID == id {
			copied := *j
			job = &copied
		}
	}
	if job == nil {
		return errors.New("unknown move")
	}
	if job.Status != JobCopied {
		return errors.New("this move is not waiting to be finished")
	}
	target, status := job.To, JobCancelled
	if deleteFromSource {
		target, status = job.From, JobFinalized
		if err := m.finalMailSync(*job); err != nil {
			return fmt.Errorf("nothing was deleted: %w", err)
		}
	}
	if err := m.call(target, "account_delete", accountArgs{Account: job.Account}, nil); err != nil {
		return err
	}
	if !deleteFromSource && job.Live {
		if err := m.call(job.From, "account_restore", accountArgs{Account: job.Account}, nil); err != nil {
			m.saveJob(id, func(j *Job) {
				j.Status, j.Step, j.Error, j.FinishedAt = status, status, "copy removed, but the source was not restored: "+err.Error(), time.Now()
			})
			return err
		}
	}
	m.saveJob(id, func(j *Job) {
		j.Status, j.Step, j.FinishedAt = status, status, time.Now()
	})
	if deleteFromSource {
		Notify("Account moved", job.Account+" moved from "+job.FromName+" to "+job.ToName+".", "info")
	} else {
		Notify("Account move cancelled", job.Account+" stays on "+job.FromName+", the copy on "+job.ToName+" was removed.", "info")
	}
	return nil
}

// finalMailSync brings over mail that reached the source while dns was still catching up
func (m *Manager) finalMailSync(job Job) error {
	fromIP, err := m.serverIP(job.From)
	if err != nil {
		return err
	}
	destIP, err := m.serverIP(job.To)
	if err != nil {
		return err
	}
	var hostKey, pub string
	if err := m.call(job.To, "ssh_hostkey", nil, &hostKey); err != nil {
		return err
	}
	if err := m.call(job.From, "ssh_key", nil, &pub); err != nil {
		return err
	}
	tag := randomHex(8)
	if err := m.call(job.To, "ssh_authorize", sshAuthorizeArgs{Pub: pub, From: fromIP, Tag: tag}, nil); err != nil {
		return err
	}
	defer m.call(job.To, "ssh_revoke", sshAuthorizeArgs{Tag: tag}, nil)
	return m.call(job.From, "mail_sync", accountArgs{Account: job.Account, DestIP: destIP, HostKey: hostKey}, nil)
}

// FinalizeMove removes the source copy once dns points at the destination
func (m *Manager) FinalizeMove(id string) error { return m.finishMove(id, true) }

// CancelMove removes the destination copy and brings a live move's source back up
func (m *Manager) CancelMove(id string) error { return m.finishMove(id, false) }

// PushSettings tells the chosen slaves to pull from us now
func (m *Manager) PushSettings(ids, groups []string) (map[string]SyncResult, map[string]string) {
	ok, failed := map[string]SyncResult{}, map[string]string{}
	for _, id := range ids {
		n, known := m.liveNodeByID(id)
		if !known {
			continue
		}
		var res SyncResult
		if err := m.rpc(n, "sync_now", groups, &res); err != nil {
			failed[n.Name] = err.Error()
			continue
		}
		ok[n.Name] = res
	}
	// a push to only some servers leaves the rest behind, so the reminder stays until all got it
	if len(failed) == 0 && len(ok) == len(m.LiveNodes()) {
		m.MarkSynced()
	}
	var parts []string
	for name, res := range ok {
		parts = append(parts, name+": "+res.Message)
	}
	for name, msg := range failed {
		parts = append(parts, name+" failed: "+msg)
	}
	if len(parts) > 0 {
		sort.Strings(parts)
		sev := "info"
		if len(failed) > 0 {
			sev = "warning"
		}
		Notify("Settings synced to servers", strings.Join(parts, "; "), sev)
	}
	return ok, failed
}

// PreviewPush asks one slave what a sync would change there
func (m *Manager) PreviewPush(id string, groups []string) ([]Change, error) {
	n, ok := m.nodeByID(id)
	if !ok {
		return nil, errors.New("unknown server")
	}
	var out []Change
	err := m.rpc(n, "sync_diff", groups, &out)
	return out, err
}
