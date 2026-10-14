[← Documentation index](../README.md#documentation)

# Dashboard

Managing agents and groups, and the ways rules reach agents.

## Agents and groups

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


## Changing rules without restarting

Every route below validates the new rules first. If validation fails, the agent keeps running the old rules and reports the error.

1. **Edit the file.** The agent checks `rules_file` every `rescan_interval` (10s by default).
2. **Dashboard, ungrouped agent.** Open the agent's Rules tab, edit, then click Validate and Save & apply.
3. **Dashboard, group.** Edit the group's rules, or change them in git and sync. The change is pushed to every member, all at once or in stages ([Safe rule rollout](rollout.md)). Members that drift are corrected automatically.
4. **API**: `curl -X PUT -H "Authorization: Bearer $TOKEN" --data-binary @rules.yaml http://agent:9300/api/v1/rules`
5. **Local rules** in `local_rules_file` (or `PUT /api/v1/rules/local`) add rules for one machine on top of whatever routes 1-4 set.

Agents write pushed rules to their `rules_file`, so the rules survive a restart.

Tailers for files that are no longer referenced are stopped. Tailers for new files are started. Files that keep the same rules are not reopened, and their suppression windows carry on.
