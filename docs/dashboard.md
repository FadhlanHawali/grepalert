[← Documentation](../README.md#documentation) · Previous: [Running agents](agents.md) · Next: [Safe rule rollout →](rollout.md)

# Dashboard and groups

The dashboard manages many agents in one place. Its own settings are in [dashboard.yaml](config-dashboard.md); everything below is done in its web UI and saved to its `data_file`.

## The pages

| Page | What it's for |
|---|---|
| **All agents** / **Ungrouped** | Every agent with its status: online, rules version, files, alerts, memory. Click one for its details: overview, rules, events, test a line, settings. |
| **Groups** | One page per group: its agents, rules, history, and a staged rollout if one is running. |
| **Alert history** | Every alert the dashboard handled, with its status: sent, silenced, failed, dropped. |
| **Silences** | Mute alerts during maintenance. |
| **Tools → Rule playground** | Try rules on sample log lines; nothing is saved or sent. |
| **Agent enrollment** | Tokens agents enroll with. Admins only. |
| **Users & access** | Local accounts, and roles for users and identity-provider groups. Admins only. |
| **Audit log** | Who changed what. Admins only. |
| **Settings** | The ops webhook for agent-down alerts, and the agent-down delay. Admins only. |

## Adding agents

- **Agents that connect to the dashboard** (recommended) appear by themselves once they enroll. See [Running agents](agents.md#connected-to-a-dashboard).
- **Connect agent** adds an agent by its address and token, when the dashboard can reach it. **Test connection** shows what the agent reports about itself. If the agent isn't running yet, **Save anyway** keeps it.

## Groups

A group is a set of agents that share one rule set. Create one with **New group**:

| Setting | Meaning |
|---|---|
| **Name, description** | |
| **Auto-add agents with labels** | e.g. `role=web, env=prod`: every connected agent with these labels joins, now and later. Leave it empty to add agents by hand. |
| **Rules come from** | **This dashboard**: edit them in the group's Rules tab, starting from an example or a copy of an agent's rules. Or **a git repository**: GitHub (including Enterprise), GitLab (including self-hosted) or a raw URL, fetched on a schedule or when you click **Sync from git**. |
| **Alerts across agents** | Combine the same alert from several agents into one: [fleet alerting](fleet-alerting.md). |
| **Agent-down alerts go to** | A webhook for this group's agent-down and agent-back alerts; default: the one in Settings. |
| **Rule changes** | Roll changes out in stages, and stop if alerts spike: [staged rollout](rollout.md). |

**How members are kept in line:**
- **Joining:** an agent that joins a group gets its rules at once.
- **Direct edits are refused:** a member's rules can't be edited directly. Change the group's instead, or give that one machine [local rules](rules.md#rules-for-one-machine-local-rules).
- **Drift is corrected:** every `reconcile_interval` (30 s), the group pushes its rules again to members that drifted (say, someone edited the file by hand) or came back online.
- **Invalid git rules are rejected:** agents keep the last good version.

**Deleting a group** makes its agents ungrouped. They keep the rules they have.

**Checking rules in CI:** to check a rules file before it reaches a group, for example on your rules repository:

```bash
grepalert-agent -check rules.yaml
```

## Roles

What each user may do depends on their role: viewer, editor or admin, for one group or all of them. See [Sign-in, roles and security](security.md).
