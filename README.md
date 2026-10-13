# grepalert

Log-based alerting without the stack. Tail a file, grep for a pattern, POST to a webhook.

grepalert is for when Loki + Promtail + Grafana, or OBM, is more setup than the problem needs. It is two static Go binaries with no database and no queue:

| Binary | Runs on | Job |
|---|---|---|
| `grepalert-agent` | every server with logs | Tails files, matches rules, suppresses floods, sends webhooks. Rules hot-reload with no restart. |
| `grepalert-dashboard` | anywhere | Web UI: connect agents, put them in groups that share one rule set (edited in the UI or synced from GitHub/GitLab), and monitor them. |

```
            GitHub / GitLab (rules.yaml)
                       │  fetch (manual or every N min)
                       ▼
 browser ──► grepalert-dashboard ──PUT /api/v1/rules──► grepalert-agent ──► webhook (Slack, Discord, …)
             (agents, groups)  ◄── GET status / events ──       │
                                                          tails /var/log/*.log
```

## Try it with Docker Compose

```bash
cd examples/compose
docker compose up -d --build
```

| URL | What it is |
|---|---|
| http://localhost:9400 | Dashboard (`admin` / `demo`, set in `.env`) |
| http://localhost:9599 | Demo webhook sink: alerts as they arrive (auto-refresh) |

The demo stack runs:

- **loggen** writes `app.log` and `access.log` into a shared volume. It adds random errors and rotates `app.log` every ~5 minutes.
- **agent-1** (`app-server-1`) **connects out** to the dashboard. It enrolls with the token from `.env` and has the labels `role=app`, `env=demo`. It starts with no rules.
- **agent-2** (`edge-proxy-1`) is a **direct** agent: the dashboard connects to it. It starts with its own nginx-5xx rule.
- **webhook-sink** receives the alerts. It also serves [examples/compose/git-repo/rules.yaml](examples/compose/git-repo/rules.yaml) over HTTP as a stand-in for a GitHub/GitLab repository.

To walk through it:

1. **app-server-1 is already listed.** It enrolled by itself, and nobody entered its address.
2. **New group** → name `app-servers` → *Auto-add agents with labels*: `role=app` → *Rules come from: A git repository* → Provider **Raw URL** → `http://webhook-sink:9599/repo/rules.yaml` → **Create group**. app-server-1 joins by its label and gets the rules from git right away.
3. **Connect agent** → URL `http://agent-2:9300`, token `demo-agent-token` → **Test connection** → **Connect**, and choose *No group*. Its own rules stay as they are, and you can edit them under the agent's **Rules** tab.
4. **New group** → `edge` → *This dashboard* → start with *A copy of edge-proxy-1's current rules*. Then click **Add an existing agent** and pick edge-proxy-1. Edit the rules in the group's **Rules** tab and click **Save & push**.
5. Edit `git-repo/rules.yaml` on disk and click **Sync from git** (or wait a minute). Every agent in `app-servers` picks up the change without a restart.
6. Under **Agent enrollment**, create a token to see the agent config it generates.
7. Under **Settings**, set the ops webhook to `http://webhook-sink:9599/hook/ops` and click **Send a test alert**. Then run `docker compose stop agent-1`: an agent-down alert arrives after the delay you set, and an agent-back alert after `docker compose start agent-1`.
8. Under **Silences**, silence group `app-servers` for 30 minutes. Its alerts show as *silenced* under **Alert history** instead of reaching the sink.

To stop the demo:

```bash
docker compose down        # add -v to also delete logs, rules and saved positions
```

To build a single image yourself: `docker build --target agent .` or `docker build --target dashboard .`

## Quick start

```bash
make build            # or: go build -o bin/ ./cmd/...
```

**Agent**, on each server:

```bash
cp examples/agent.yaml examples/rules.yaml /etc/grepalert/
export GREPALERT_AGENT_TOKEN=$(openssl rand -hex 16) SLACK_WEBHOOK_URL=https://hooks.slack.com/...
grepalert-agent -config /etc/grepalert/agent.yaml
```

**Dashboard**:

```bash
cp examples/dashboard.yaml .
export GREPALERT_DASHBOARD_PASSWORD=...
grepalert-dashboard -config dashboard.yaml     # http://localhost:9400
```

Then, in the UI, create an enrollment token (**Agent enrollment**) for your agents, or connect to them by address (**Connect agent**), and create groups (**New group**).

## Agents that connect to the dashboard (recommended)

In most company networks the dashboard cannot reach the servers: NAT, firewalls, separate VPCs. So the agent connects **out** to the dashboard instead, and needs no inbound port:

```yaml
# /etc/grepalert/agent.yaml
dashboard:
  url: https://grepalert.example.com
  enrollment_token: ${GREPALERT_ENROLL_TOKEN}   # used once, at first start
labels:
  role: web
  env: prod
rules_file: /var/lib/grepalert/rules.yaml
```

1. **Enroll.** On first start the agent presents the enrollment token. The dashboard gives it its own credential, which the agent saves to `identity_file` (default: next to `rules_file`, mode `0600`). After that the token is no longer needed. A restarted agent reuses its identity, so it never shows up twice.
2. **Connect.** The agent keeps one HTTPS long-poll open to `/agent/v1/poll`. The dashboard sends its usual API calls (status, rules, events) down that connection, and the agent runs them against its own API in-process. Nothing listens on the server unless you set `listen`. HTTP proxies are honoured via `HTTPS_PROXY`, and custom CAs via `dashboard.ca_file`.
3. **Join groups by label.** A group with *Auto-add agents with labels* `role=web,env=prod` picks up every agent that has those labels. It pushes them its rules right away, including agents that enroll later. If several groups match, the first by name wins. An admin can override this per agent (Settings → Group). Every agent also gets an `os` label automatically.

**Tokens.** Create enrollment tokens under **Agent enrollment** in the UI, with an optional expiry. They are stored only as hashes and shown once. For automation (Ansible, Terraform, VM images), you can instead set a fixed `enrollment_token` in the dashboard config.

**Revoking access.** Revoking a token stops *new* enrollments. To cut off an agent that already enrolled, use **Revoke access** in its Settings. The dashboard then refuses its connection and stops managing it. **Allow again** restores it within about 15 seconds.

**Resilience.** Agents keep alerting with their last rules while the dashboard is down, and reconnect by themselves (with backoff, up to 30 s). If the dashboard's data is lost, agents notice they are unknown and enroll again with their token.

Measured with 10 VMs that the dashboard could not reach directly:
- All 10 enrolled within a second of starting.
- A new `role=api` group reached all 10 in **111 ms**, and a rules edit reached them in **72 ms**.
- After a dashboard restart, all 10 were back online in 7.6 s.

## Dashboard: agents and groups

Agents and groups are managed in the UI and saved to `data_file`. That file holds direct-agent tokens, git tokens and enrollment-token hashes, so it is written with `0600` permissions; keep it private. Token fields also accept `${ENV_VAR}`, which is read from the dashboard's environment when the dashboard uses the token.

- **Connect agent** (direct mode) asks for the agent's URL and token. Use it when the dashboard can reach the agent. **Test connection** shows what the agent reports about itself. If you leave the name empty, the agent's own `name` is used. If the agent isn't reachable yet, you can still choose **Save anyway**.
- **Group**: a set of agents that share one rule set. The rules come from one of two places:
  - **This dashboard**: write the rules in the group's Rules tab. You can start from an example or from a copy of an agent's current rules. **Save & push** applies them to every member.
  - **A git repository**: GitHub (including Enterprise), GitLab (including self-hosted), or a raw URL. The dashboard fetches on a schedule or when you click **Sync from git**. If the new content in git is invalid, it is rejected, and agents keep the last good version.
- **Members**: an agent that joins a group gets the group's rules immediately. Members are managed: editing a member's rules directly is refused. Every `reconcile_interval` (30s by default), the dashboard re-pushes the group's rules to members that drifted, for example after someone edited the file by hand, or that came back online.
- **Ungrouped agents** keep their own rules, and you edit them one agent at a time.
- **Deleting a group** makes its agents ungrouped. They keep the rules they have.

To check a rules file before shipping it (for example in CI on your rules repo):

```bash
grepalert-agent -check rules.yaml
```

## Fleet-level alerting

Agents that connect to the dashboard hand their alerts to it, instead of each sending to the webhook alone. The dashboard sees the whole fleet, so it can:

- **Combine the same alert across hosts.** When 50 servers log `DB_TIMEOUT` at once, the dashboard waits a few seconds to gather the reports (`wait`, 5 s by default) and sends one alert: *"errors (DB_TIMEOUT) on 50 hosts (web-01, web-02 … +40)"*. Repeats are then held back for the group's `window` (5 min by default). When the window closes, one summary follows: *"26,788 more matches on 50 hosts were suppressed"*. Set this per group under **Edit → Alerts across agents**. Each agent's own `suppress` still applies first.
- **Silence alerts for maintenance.** Under **Silences**, mute a group (optionally only some rules or agents) for a set time, with a reason. Silenced alerts appear in the history but are not sent, and the silence ends by itself. Agents also receive active silences, so silences hold even when an agent has to send alerts itself. The rule name `agent-down` targets agent-down alerts.
- **Alert when an agent goes quiet.** If an agent stops reporting for the delay set under **Settings** (default 2 min), the dashboard sends an *agent-down* alert, and an *agent-back* alert when it returns. These go to the ops webhook in Settings, or to a URL set on the group. Revoked and removed agents never alert.
- **Keep a history.** **Alert history** lists every alert the dashboard handled, with its status (sent, silenced, failed, dropped) and the hosts involved.

**If the dashboard is down, alerts still go out.** When an agent cannot reach the dashboard for `dashboard.fallback_after` (default 15 s), it sends alerts directly, as before, marked *"sent by the agent: dashboard unreachable"* and `via: "agent"`. It switches back on its own. Right after an agent starts, alerts wait up to that long for the first connection instead of bypassing the dashboard.

Webhook URLs such as `${SLACK_WEBHOOK_URL}` are resolved on the agent and sent to the dashboard with each alert, over the agent's authenticated connection. The dashboard needs none of those secrets and never stores them.

Measured with 10 VMs, each logging 5 error codes:
- **Dashboard up:** 250 agent alerts became 5 webhook calls ("on 10 hosts"), then 5 window summaries.
- **Agent stopped:** the agent-down alert arrived 35 s later (30 s setting), and agent-back 2 s after restart.
- **Dashboard stopped for 55 s:** all 69 alerts from that time were delivered directly by the agents.

Limits to know about:
- **Restarting the dashboard drops in-memory state.** Open windows are lost, so their summaries are never sent, and the alert history starts empty again. Silences and settings are saved.
- **Only connected agents are combined.** Direct agents (added by URL) still send their own alerts.
- **Duplicates are possible around a fallback.** Delivery is at-least-once: an alert can be sent twice if the dashboard received it just as the agent gave up on it.

## Rules

```yaml
webhooks:
  - name: slack
    url: ${SLACK_WEBHOOK_URL}        # resolved from the agent's environment, so it stays out of git
    template: '{"text": {{ json .Text }}}'

rules:
  - name: nginx-5xx
    files: ["/var/log/nginx/access.log", "/var/log/app/*.log"]   # globs are re-scanned
    pattern: '" (?P<status>5\d\d) '      # Go RE2 regexp
    exclude: 'GET /healthz'              # optional
    severity: critical
    labels: {team: web}
    webhooks: [slack]
    suppress:
      window: 5m
      max_alerts: 1
      group_by: status                   # a separate window per status code
```

See [examples/rules.yaml](examples/rules.yaml) for more examples.

### Suppression

Without a `suppress` block, every matching line sends an alert. With one:

- Each window lets up to `max_alerts` alerts through (default 1). Matches after that are counted but not sent.
- `group_by` names a capture group in the pattern. Each distinct value gets its own window, so `E1` errors do not hide `E2` errors.
- When a window closes and some matches were held back, one summary alert is sent, for example "49 more matches were suppressed (E1). Last: …". Set `summary: false` to turn this off.

### Absence rules

Error rules can't catch an app that hangs or a cron job that stopped, because those write nothing. An absence rule alerts when an expected line has *not* appeared for a while:

```yaml
  - name: no-heartbeat
    files: ["/var/log/myapp/app.log"]
    absent: 'heartbeat OK'    # instead of pattern:
    for: 10m
    webhooks: [ops]
```

It alerts once after `for` has passed without a matching line, and once more ("recovered") when the line shows up again. A newly loaded rule gets its full `for` before it can fire.

### Webhook payload

With no `template`, the agent POSTs the alert as JSON:

```json
{"kind":"match","agent":"web-01","host":"web-01","rule":"nginx-5xx","severity":"critical",
 "labels":{"team":"web"},"file":"/var/log/nginx/access.log","line":"…","groups":{"status":"502"},
 "group_key":"502","time":"2026-10-12T20:41:37+07:00","suppressed":0,"text":"CRITICAL [web-01] nginx-5xx matched in …"}
```

`kind` is `match`, `summary`, `absent`, `recovered`, `fleet_summary`, `agent_down` or `agent_up`. Fleet alerts also carry `hosts`, `host_count` and `group`; `via` says whether the dashboard or the agent delivered it, and `id` lets receivers drop duplicates. A `template` is a Go `text/template` rendered with the same fields (`.Rule`, `.Line`, `.Groups.status`, `.Text`, …). Template helpers: `json` (a safely quoted JSON string), `upper`, `lower`, `trunc N`.

## Changing rules without restarting

Every route below validates the new rules first. If validation fails, the agent keeps running the old rules and reports the error.

1. **Edit the file.** The agent checks `rules_file` every `rescan_interval` (10s by default).
2. **Dashboard, ungrouped agent.** Open the agent's Rules tab, edit, then click Validate and Save & apply.
3. **Dashboard, group.** Edit the group's rules, or change them in git and sync. The change is pushed to every member, and members that drift are corrected automatically.

Agents write pushed rules to their `rules_file`, so the rules survive a restart.
4. **API**: `curl -X PUT -H "Authorization: Bearer $TOKEN" --data-binary @rules.yaml http://agent:9300/api/v1/rules`

Tailers for files that are no longer referenced are stopped. Tailers for new files are started. Files that keep the same rules are not reopened, and their suppression windows carry on.

## Memory: built not to grow

This is the main lesson from OBM. Every structure that could grow with log volume has a hard limit:

| Concern | How grepalert bounds it |
|---|---|
| Reading lines | One fixed 32 KB read buffer per file, reused. Lines that don't match cause **0 allocations** (`go test -bench . ./internal/agent`). |
| Huge or binary lines | Each line is cut at `max_line_bytes` (16 KB). The agent never buffers a whole line past that. |
| Suppression state | An entry is freed when its window closes. There is a hard cap of 10,000 keys; past that, new keys share one overflow bucket. |
| Alert storms / slow webhooks | A bounded queue (`queue_size`) and a fixed set of workers. When the queue is full, alerts are **dropped and counted** rather than queued in memory. |
| Event log | A fixed-size ring buffer (`event_buffer`). |
| Rule reloads | Rules are compiled once and swapped atomically. Tailers and suppression state for removed rules are released. |
| Go runtime | `memory_limit` (64 MiB by default) sets the GC soft limit. The dashboard shows heap, OS memory, GC count, and goroutines for each agent. |

Soak test: 1,000,000 lines with a unique `group_by` value on every line (the worst case for suppression). Heap stayed between 3 and 5 MB, process memory stayed at about 26 MB, the goroutine count did not change, and suppression state went back to 0 once the windows closed.

## Performance with multi-GB logs

Measured on an i5-13400F with 4 rules per line, one of them `(?i)panic|fatal|segfault`:

| Test | Result |
|---|---|
| Start on an existing 5 GB file | Tailing from its end in 164 ms. File size does not matter, because the agent seeks to the end. |
| Read a whole 5 GB file (22.5M lines, the worst-case catch-up) | **18 s, ~310 MB/s**, process memory 11–17 MB |
| Live tail of a log written at 100 MB/s for 60 s (6 GB), rotating every 2 GB | Kept up. All 27.0M lines were read, the read position finished exactly at the end of the file, and memory was ~17 MB. |
| 10 VMs, each writing 10 MB/s and rotating at 1 GB, all in one dashboard group, for 3 min | Every agent kept up and read to the exact end of its file. Each used 18–22 MB. A rules push to all 10 under load took 560 ms; the dashboard overview took ≤ 72 ms. |

**Why case-insensitive rules stay fast.** Go's regexp engine is slow on case-insensitive alternations such as `(?i)panic|fatal`, at about 8 µs per line. So each rule first gets a *literal prefilter*: the plain strings at least one of which must appear in any match (`panic`, `fatal`, …). The agent checks those with a fast substring search and runs the regexp only on lines that pass, which brings the cost down to ~0.2 µs per line. The prefilter never skips a line the regexp would match: this is covered by a randomized cross-check over 3.6M lines and by fuzzing.

To get the most out of it, give rules a literal anchor where you can. `ERROR code=\w+` is fast. A rule with no literal text at all, such as `\d{3}ms$`, has to run the regexp on every line.

### One agent, many files

An agent does not start one goroutine per file. A small pool of reader workers (`read_workers`, 4 by default) polls all files:
- **Busy files** are checked every `poll_interval` (500 ms).
- **Idle files** back off to `idle_poll_interval` (2 s). This bounds how late the first line after a quiet period is noticed.
- **Backlogs** are read in turns of at most 4 MB, so a file with a large backlog cannot delay alerts on the others.
- **Stopping** gives every file a final read.

Measured with one agent in a container with a 64 MB memory limit and 4 rules:

| Files | Load | Result |
|---|---|---|
| 1 / 4 / 8 / 16 | Backlog of 1–8 GB, read from the start | 168 / 683 / 673 / 940 MB/s. CPU stays at about 4 cores (`read_workers`), and every file is read to the exact last byte. |
| 100 | 1 MB/s each (100 MB/s total), live | Kept up, at ~40% of one core and 11 MB of container memory |
| 500 | 0.1 MB/s each, live | Kept up, at ~45% CPU; 23 goroutines; 13 MB of container memory |
| 500 | idle | **1.3% CPU**, 2 GC runs per 10 s |

Raise `read_workers` to catch up faster on many files at once. It scales linearly up to the number of cores; with 8 cores, 8 files were read at 1.3 GB/s. Lower it to protect the applications running on the same VM.

### Load-test your own setup

[examples/loadtest](examples/loadtest/compose.yaml) runs N simulated VMs. Each one runs `loadgen`, which writes realistic logs at a set rate with rotation, next to an agent. A dashboard and a webhook sink are included.

```bash
cd examples/loadtest
LOG_RATE=20MB docker compose up -d --build --scale vm=10
```

Then connect the VMs (`http://grepalert-loadtest-vm-N:9300`, token `loadtest-token`) in the dashboard at http://localhost:9410 (`admin` / `loadtest`). On a real VM you can also run `loadgen` alone to see what an agent can sustain:

```bash
go run ./examples/loadtest/loadgen -out /tmp/app.log -rate 50MB -rotate 1GB
```

## Log rotation

The agent polls each file (500 ms by default, set with `poll_interval`). That works the same way on Linux, macOS and Windows.

- **rename + create** (logrotate default): the agent finishes reading the old file, then reads the new one from the start.
- **copytruncate**: the agent sees the file shrink and starts again from offset 0.
- **Files that don't exist yet**: the agent waits for them, then reads them from the start.
- **First start**: files are read from the end (`start_from: end`), so old history doesn't set off a burst of alerts.

### Restarts don't miss lines

Every 5 seconds (`state_interval`), and again on shutdown, the agent saves its read position for each file to `state_file`. By default that is `grepalert.state.json` next to the rules file. Each position records:

- the offset just past the last complete line, so a half-written line is read in full after a restart
- the file's identity (inode, or the Windows file index)
- a hash of the first 1 KB of the file

On restart the agent checks whether the file is still the one it was reading:

- **Same file**: it resumes at the saved offset. Lines written while it was down are alerted on.
- **Rotated while down**: it looks for the old file under a rotated name next to it (`app.log.1`, `app.log-20261012`, … but not compressed `.gz` files). It reads the rest of that old file, then reads the new file from the start. This works for both rename and copytruncate rotation.
- **Replaced and the old file is gone**: it reads the new file from the start.

Delivery is at-least-once. After a crash, lines read since the last save (at most `state_interval` worth) are processed again and can alert twice. A clean shutdown saves exact positions and spends up to 5 seconds delivering alerts that are still queued. Suppression windows are kept in memory, so they start fresh after a restart.

## Security

- **Connected agents** open no port (`listen` is off by default when `dashboard.url` is set). Each agent has its own revocable credential; the enrollment token is only used once. Put the dashboard behind HTTPS (`tls_cert`/`tls_key`, or a reverse proxy).
- **Direct agents' API**: requests need `Authorization: Bearer <token>`. `/healthz` is the only endpoint that doesn't. Set `tls_cert`/`tls_key`, or keep the agent on a private network.
- **Dashboard**: HTTP basic auth (`auth.password`). Write requests need an `X-Requested-With` header, which blocks CSRF.
- **Secrets**: keep webhook URLs and tokens in environment variables (`${VAR}`), not in the rules repository.

## Agent API

| Method & path | Purpose |
|---|---|
| `GET /healthz` | Liveness check (no auth). |
| `GET /api/v1/status` | Files, offsets, per-rule counters, webhook stats, memory. |
| `GET /api/v1/rules` | The active rules YAML. The `X-Rules-Hash` header identifies the version. |
| `PUT /api/v1/rules` | Validate, persist and apply new rules. |
| `POST /api/v1/rules/validate` | Validate rules without applying them. |
| `GET /api/v1/events?limit=N` | Recent alerts, rule changes and errors. |
| `POST /api/v1/test` | `{"line": "..."}`: lists which rules would match that line. |

## Layout

```
cmd/grepalert-agent       agent binary
cmd/grepalert-dashboard   dashboard binary
internal/rules            rule format, validation, compilation (shared)
internal/agent            tailer, matching/suppression engine, notifier, API
internal/alert            alert format, webhook rendering and bounded sender (agent + dashboard)
internal/dashboard        agent/group store, group sync, git fetch, fleet alerting, embedded web UI
examples/                 agent.yaml, rules.yaml, dashboard.yaml, systemd unit
```
