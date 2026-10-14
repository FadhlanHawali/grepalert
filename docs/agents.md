[← Documentation index](../README.md#documentation)

# Agents

How agents connect to the dashboard, what they keep on disk, and how they follow log files through rotation and restarts.

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


## What an agent keeps on disk

| File (next to `rules_file`) | What it is |
|---|---|
| `rules.yaml` (`rules_file`) | The rules the agent runs: its own, or its group's once it joins one (a group push overwrites it) |
| `rules.local.yaml` | Extra rules for this machine only, run on top. Groups never touch it |
| `rules.before-group.yaml` | The agent's own rules, saved when a group first replaced them |
| `grepalert.state.json` | Read positions, so restarts neither miss nor repeat lines |
| `grepalert.identity.json` | The credential an enrolled agent got from the dashboard |
| `grepalert.silences.json` | The last silences from the dashboard |

**Joining and leaving a group.**
- **Joining:** the group's rules replace the agent's own in `rules.yaml`. Before the first replacement, the agent saves its own rules to `rules.before-group.yaml`.
- **Leaving:** the agent keeps running the group's rules until you restore. When you move it out of its group, the dashboard offers to restore its own rules; you can also do it later from the agent's **Rules** tab.
- **Editing instead of restoring:** if you edit the agent's rules directly instead, those become its own rules, and the saved copy is dropped.

**If the dashboard is down.**
- **Rules:** agents keep running the rules on their disk, including after a restart.
- **Alerts:** they go straight to the webhooks after `fallback_after` (15s).
- **Silences:** they still hold, even across an agent restart.
- **Recovery:** when the dashboard is back, agents reconnect by themselves and are brought up to date.


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
