---
name: deploy-openadmin
description: Build the OpenAdmin Go binary and deploy it to the test server's 'admin' systemd service, then smoke-test it live.
---

# Deploy OpenAdmin to the test server

Deploys the current working tree of this repo to a test server by
replacing the `admin` systemd service's binary, then verifies it actually
works by logging into the live app.

**Never write the server address, SSH password, or app login credentials
into any file in this repo.** Ask for them in the conversation each time
this skill runs, use them only inline in the Bash commands that need them,
and don't echo them back or persist them anywhere.

## Step 0 — Get connection details for this run

Ask the user (if not already given earlier in this conversation) for:
1. The server's IP address or hostname (call it `$SERVER` below).
2. The root SSH password for that server.
3. OpenAdmin login credentials to use for the post-deploy smoke test (a
   username/password that can log into `https://$SERVER:2087` — confirm
   the port too, in case it's not 2087 on this server).

## Step 1 — Build and test locally first

From the repo root:

```bash
go build ./... && go test ./... 2>&1 | tail -60
```

Fix or flag any failures before deploying. (One known pre-existing failing
test, `TestComposeServicesForUserParsesResolvedConfig`, is unrelated to
this repo's web/handler code — don't let it block a deploy on its own,
but do check `git stash` to confirm it already fails on a clean tree if
you're unsure whether your change caused it.)

Then cross-compile for the server (linux/amd64, no cgo needed here):

```bash
GOOS=linux GOARCH=amd64 go build -buildvcs=false -o /tmp/claude-*/scratchpad/openadmin-amd64 ./cmd/openadmin
```

(Use this session's actual scratchpad path, not a literal glob.)

## Step 2 — Copy the binary to the server

The server has no `rsync` — use `scp`. Do the SSH password export and the
command in the *same* Bash call so the env var doesn't leak into a
separate, persisted shell state:

```bash
export SSHPASS='<password from step 0>'
sshpass -e scp -o StrictHostKeyChecking=accept-new \
  <scratchpad>/openadmin-amd64 \
  root@<server>:/usr/local/admin/openadmin-amd64.new
```

## Step 3 — Swap the binary in and restart

```bash
export SSHPASS='<password>'
sshpass -e ssh root@<server> '
set -e
cp /usr/local/admin/openadmin-amd64 /usr/local/admin/openadmin-amd64.bak-$(date +%Y%m%d%H%M%S)
chmod +x /usr/local/admin/openadmin-amd64.new
mv /usr/local/admin/openadmin-amd64.new /usr/local/admin/openadmin-amd64
systemctl restart admin
sleep 2
systemctl is-active admin
systemctl status admin --no-pager | head -15
'
```

Confirm `systemctl is-active` prints `active` and the status block shows no
crash-loop (a healthy process has been running only a couple seconds,
which is expected right after a restart — the concerning sign is repeated
restarts within that status output).

If it doesn't come up cleanly, the previous binary is at
`/usr/local/admin/openadmin-amd64.bak-<timestamp>` and can be moved back
into place with `systemctl restart admin` afterward.

## Step 4 — Smoke-test it live (log in and check it actually works)

Prefer an actual browser check if a browser-automation tool/skill (e.g.
`claude-in-chrome`) is available in this environment: load that skill
first, open `https://<server>:2087/login` (or whatever port was confirmed
in Step 0), log in with the credentials from Step 0, navigate to whatever
page is relevant to the change you just deployed, and take a screenshot to
visually confirm it renders correctly and the feature works.

If no browser tool is available, fall back to this curl-based login flow
(this exact sequence is proven to work against this app's gorilla/csrf
setup — the `-e` Referer flag matters, the app rejects HTTPS POSTs without
a matching Referer):

```bash
cd <scratchpad>
BASE="https://<server>:<port from step 0>"

# 1. GET the login page, capture cookies + csrf token
login_page=$(curl -sk -c cookies.txt -e "$BASE/login" "$BASE/login")
csrf=$(echo "$login_page" | grep -oP 'name="csrf_token" value="\K[^"]+' | head -1 \
  | python3 -c "import sys,html; print(html.unescape(sys.stdin.read().strip()))")

# 2. POST credentials (from Step 0) + csrf token, same cookie jar
curl -sk -b cookies.txt -c cookies.txt -e "$BASE/login" -o login_resp.html \
  -w "login POST status: %{http_code}\n" \
  -X POST "$BASE/login" \
  --data-urlencode "username=<username>" \
  --data-urlencode "password=<password>" \
  --data-urlencode "csrf_token=$csrf"
# Expect "login POST status: 303" (redirect on success).

# 3. Fetch a page relevant to what changed and check it looks right
curl -sk -b cookies.txt "$BASE/<relevant-page>" | grep -o "<whatever confirms the feature>"
```

Pick the page/grep target based on whatever was actually changed this
session (e.g. `/settings/notifications` for SMTP-section changes,
`/users/<username>` for user-detail-page changes).

## Step 5 — Report back

Summarize concisely: build/test result, deploy result (service active or
not), and the smoke-test result (what you actually saw on the live page).
If anything failed, say exactly what and at which step — don't declare
success unless the live page genuinely showed the expected content.
