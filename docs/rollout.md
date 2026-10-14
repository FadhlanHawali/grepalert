[← Documentation index](../README.md#documentation)

# Safe rule rollout

A bad rule can page everyone at once, or quietly match nothing. Four tools in the dashboard help prevent both.

**Versions and one-click rollback.** Every change to a group's rules is saved as a version: edits, git syncs, rollbacks and aborted rollouts.
- The group's **History** tab shows each version with who made it and how. **View** shows what rolling back to it would change.
- **Roll back to this** sends that version to every agent at once. It skips staged rollout, because a rollback is for when something is already wrong.
- For a git group, a rollback also pauses following git. Otherwise the next fetch would bring the bad version back. **Sync from git** resumes it.
- The last 50 versions are kept per group, plus any version an agent still runs.

**Staged rollout.** Turn it on in the group's settings (**Edit → Rule changes**). A rule change then reaches the group in stages: by default 1 agent, then 10%, then everyone.
- **Watching a stage.** Each stage is watched for a while (5 minutes by default). The dashboard compares the alerts per agent on the new version with the agents still on the previous version.
- **Automatic stop.** The rollout stops if the new version raises more than 3× the alerts, with at least 10 alerts so a few stray matches don't count. It also stops if an agent refuses the new rules (for example an unset `${VAR}`). Every agent then goes back to the previous version, and the agent-down webhook gets a `rollout` alert.
- **Manual control.** Editors can move to the next stage now, send to everyone now, try again with other first agents, or abort and go back.
- **What the group page shows.** The page shows the stage, which agents run which version, and a log of every decision. The agent list marks each agent with *new version* or *previous version*.
- **Choice of first agents.** They are picked by a hash of the rollout and the agent name, so each rollout starts somewhere different.

**Dry run.** In a group's Rules tab, **Dry run** replays the last 15 minutes, hour or 3 hours of every member's logs. It uses the rules in the editor, or for a git group the latest rules in git. Nothing is sent.
- **What you see.** For each rule: how many alerts it would have sent, next to what the current rules sent for the same lines, with example alerts. For example, *"These rules would have sent 340 alerts; the current rules sent 12."*
- **Accurate timing.** Suppression windows, `group_by`, and absence rules are simulated with each line's time. The agent knows those times because it records where each file was once a minute; this costs a fixed ~3 KB per file.
- **Bounded cost.** Lines are streamed, at most 256 MB per file is replayed, and one dry run runs at a time per agent. Files with no history yet (new to the agent, or the agent just started) are read from their last 16 MB, and the results say their times are estimated.

**Local rules (per-VM overlay).** A machine can have extra rules on top of its group's rules, for example for a file only that server has.
- **Where they live.** They are kept on the agent in `local_rules_file` (default `rules.local.yaml` next to `rules_file`). Edit them in the agent's **Rules** tab, or on disk.
- **Add only.** Local rules can use the group's webhooks but can only add rules. A local rule with the same name as a group rule is refused. If the group later adds a rule with that name, the group wins and the agent reports the local rules as rejected.
- **No effect on sync.** Local rules don't change the group version the agent reports, so the agent stays in sync.
