# Changelog

## [0.1.0] - 2026-10-14

The first release: an agent that turns log lines into webhook alerts, and an optional dashboard to run a fleet of them.

### Agent
- Tails log files, including `*.log` globs, and follows rotation and truncation. It saves read positions, so a restart neither misses nor repeats lines.
- Rules: a regex pattern with optional `exclude`, named captures (`(?P<name>...)`), severity and labels. Absence rules alert when an expected line stops appearing, and again when it is back.
- Suppression per rule and per captured value: a set number of alerts per time window, then one "N more were held back" summary.
- Webhooks with Go templates, so a message can be built from the values a pattern pulled out of the line. `${VAR}`s keep URLs and secrets out of rule files.
- Rule changes apply without a restart: from the rules file (checked every 10 s), the API, or a dashboard group. Invalid rules are rejected and the last good ones keep running. Validate a file with `grepalert-agent -check`.
- Local rules (`rules.local.yaml`): extra rules for one machine, on top of its group's.
- Memory stays flat, whatever the log volume: everything is bounded, with a 64 MiB soft limit by default. Tested with multi-GB files and many files per agent.
- Runs on its own, or connected to a dashboard. Connected agents enroll with a token and connect out, so they need no inbound port.

### Dashboard
- Groups of agents share one rule set, edited in the UI or synced from GitHub, GitLab or a URL. Agents join a group by label.
- Safe rule changes:
  - every version is kept, with a diff and one-click rollback
  - staged rollout (one agent, then 10%, then all) that stops by itself when the new version's agents alert far more than the others
  - a dry run of draft rules against the last hours of real logs
- Rule playground: try rules on sample log lines and see what matched, what was captured, and the exact webhook body.
- Fleet alerting: the same alert from many hosts becomes one, with silences, alert history, and agent-down/agent-back alerts.
- Agents keep working when the dashboard is down. They alert the webhook directly after 15 s, and their silences hold even across a restart.
- Sign-in with SSO (OpenID Connect) and/or local accounts. Viewer, editor and admin roles, per group or global. An audit log with diffs of every change.
- A web UI (React) embedded in the binary, under a strict Content-Security-Policy.

### Distribution
- Static binaries for Linux, macOS and Windows (amd64/arm64).
- Container images for linux/amd64 and linux/arm64: `ghcr.io/fadhlanhawali/grepalert-agent` and `ghcr.io/fadhlanhawali/grepalert-dashboard`.

[0.1.0]: https://github.com/FadhlanHawali/grepalert/releases/tag/v0.1.0
