# grepalert

Log-based alerting without the stack. Tail a file, grep for a pattern, POST to a webhook.

**On its own.** One agent and a rules file on the server is all it takes:

![A standalone agent: it reads rules.yaml from the server, matches a log line, pulls out values and posts the message straight to the webhook; editing rules.yaml reloads it without a restart; repeats are suppressed and summed up once](docs/assets/standalone.gif)

**With the dashboard.** Manage many agents: rules come from git, and the same alert from many servers becomes one:

![How grepalert works: rules go from git through the dashboard to every agent; an agent matches a log line, pulls values out of it and sends your message to a webhook; the same alert from many servers becomes one; repeats are suppressed](docs/assets/how-it-works.gif)

**If the dashboard goes down.** Agents keep alerting. They work from the rules, read positions and silences on their own disk, even across a restart:

![The dashboard goes down: the agent keeps running from its disk and, after 15 seconds, alerts the webhook directly while silenced rules stay silent; the agent restarts during the outage, resumes from its saved position so a line written meanwhile still alerts; the dashboard comes back and the agent reconnects on its own](docs/assets/dashboard-down.gif)

grepalert is for when Loki + Promtail + Grafana is more setup than the problem needs. It is two static Go binaries with no database and no queue:

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

## Quick start

```bash
make build            # builds the web UI (needs Node.js 20+) and both binaries into bin/
```

The dashboard's web UI is embedded in its binary, so each program is still a single file to copy. To build the agent only, `go build ./cmd/grepalert-agent` is enough (no Node.js needed).

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

## Try it with Docker Compose

```bash
cd examples/compose
docker compose up -d --build
```

Open http://localhost:9400 (`admin` / `demo`). Three app servers enroll themselves and join a group whose rules come from a stand-in git repository, with staged rollout on. Alerts arrive at http://localhost:9599. [The demo walkthrough](docs/demo.md) shows what to try. For single sign-on, there is also [a Keycloak example](docs/security.md#sign-in-roles-and-audit-log).

## Documentation

| Topic | What's in it |
|---|---|
| [Demo walkthrough](docs/demo.md) | The Docker Compose demo, step by step |
| [Agents](docs/agents.md) | Enrolling agents, labels, files an agent keeps on disk, groups, the dashboard being down, log rotation, restarts |
| [Dashboard](docs/dashboard.md) | Agents and groups, rules from the UI or GitHub/GitLab, how changes reach agents |
| [Rules](docs/rules.md) | Rule format, suppression, absence rules, webhook payloads and templates, extracting values from a line |
| [Safe rule rollout](docs/rollout.md) | Versions and rollback, staged rollout, dry run, per-machine local rules |
| [Fleet-level alerting](docs/fleet-alerting.md) | One alert across many hosts, silences, agent-down alerts |
| [Security, sign-in and audit log](docs/security.md) | SSO (OIDC) and local accounts, roles, audit log, hardening |
| [Memory and performance](docs/performance.md) | Why the agent's memory stays flat, multi-GB logs, many files, load testing |
| [Agent API](docs/api.md) | The agent's HTTP API |
| [Development](docs/development.md) | Code layout, building, working on the web UI |
