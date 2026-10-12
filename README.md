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
- **agent-1** (`app-server-1`) starts with no rules.
- **agent-2** (`edge-proxy-1`) starts with its own nginx-5xx rule.
- **webhook-sink** receives the alerts. It also serves [examples/compose/git-repo/rules.yaml](examples/compose/git-repo/rules.yaml) over HTTP as a stand-in for a GitHub/GitLab repository.

The dashboard starts empty. To walk through it:

1. **New group** → name `app-servers` → *Rules come from: A git repository* → Provider **Raw URL** → `http://webhook-sink:9599/repo/rules.yaml` → Sync every minute → **Create group**. The rules are fetched immediately.
2. In the group, click **Connect agent** → URL `http://agent-1:9300`, token `demo-agent-token` → **Test connection** → **Connect**. The group's rules are pushed to the agent right away.
3. **Connect agent** again with `http://agent-2:9300`, and choose *No group*. Its own rules stay as they are, and you can edit them under the agent's **Rules** tab.
4. **New group** → `edge` → *This dashboard* → start with *A copy of edge-proxy-1's current rules*. Then click **Add an existing agent** and pick edge-proxy-1. Edit the rules in the group's **Rules** tab and click **Save & push**.
5. Edit `git-repo/rules.yaml` on disk and click **Sync from git** (or wait a minute). Every agent in `app-servers` picks up the change without a restart.

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

Then, in the UI, connect your agents (**Connect agent**: URL + token) and create groups (**New group**).

## Dashboard: agents and groups

Agents and groups are created in the UI and saved to `data_file`. That file holds agent and git tokens, so it is written with `0600` permissions; keep it private. Token fields also accept `${ENV_VAR}`, which is read from the dashboard's environment when the dashboard uses the token.

- **Connect agent** asks for the agent's URL and token. **Test connection** shows what the agent reports about itself. If you leave the name empty, the agent's own `name` is used. If the agent isn't reachable yet, you can still choose **Save anyway**.
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

### Webhook payload

With no `template`, the agent POSTs the alert as JSON:

```json
{"kind":"match","agent":"web-01","host":"web-01","rule":"nginx-5xx","severity":"critical",
 "labels":{"team":"web"},"file":"/var/log/nginx/access.log","line":"…","groups":{"status":"502"},
 "group_key":"502","time":"2026-10-12T20:41:37+07:00","suppressed":0,"text":"CRITICAL [web-01] nginx-5xx matched in …"}
```

`kind` is either `match` or `summary`. A `template` is a Go `text/template` rendered with the same fields (`.Rule`, `.Line`, `.Groups.status`, `.Text`, …). Template helpers: `json` (a safely quoted JSON string), `upper`, `lower`, `trunc N`.

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

- **Agent API**: requests need `Authorization: Bearer <token>`. `/healthz` is the only endpoint that doesn't. Set `tls_cert`/`tls_key`, or keep the agent on a private network.
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
internal/dashboard        agent/group store, group sync, git fetch, embedded web UI
examples/                 agent.yaml, rules.yaml, dashboard.yaml, systemd unit
```
