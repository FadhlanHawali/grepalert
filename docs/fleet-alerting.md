[← Documentation](../README.md#documentation) · Previous: [Safe rule rollout](rollout.md) · Next: [Kubernetes →](kubernetes.md)

# Fleet-level alerting

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
