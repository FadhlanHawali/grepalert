[← Documentation](../README.md#documentation) · Previous: [Demo walkthrough](demo.md) · Next: [Releasing →](releasing.md)

# Development

Code layout, building from source, and working on the web UI.

## Layout

```
cmd/grepalert-agent       agent binary
cmd/grepalert-dashboard   dashboard binary
internal/rules            rule format, validation, compilation (shared)
internal/agent            tailer, matching/suppression engine, notifier, API
internal/alert            alert format, webhook rendering and bounded sender (agent + dashboard)
internal/dashboard        agent/group store, group sync, git fetch, fleet alerting, rollouts; embeds the built UI (web/dist)
ui/                       dashboard web UI: React + TypeScript, built with Vite
examples/                 agent.yaml, rules.yaml, dashboard.yaml, systemd unit
```

## Working on the web UI

The UI lives in `ui/`:

| Path | What |
|---|---|
| `src/pages/` | one component per page: group, agent, alerts, silences, settings, access, audit |
| `src/dialogs/` | the forms in dialogs |
| `src/state.tsx` | the signed-in user, the polled overview, navigation |
| `src/api.ts` | the API client |
| `src/types.ts` | the API's JSON shapes |

`make ui` builds it into `internal/dashboard/web/dist`, which `go build` embeds. The build output is not committed. A dashboard built without it still runs its API, and its page explains how to build the UI.

For hot reload while editing:

```bash
grepalert-dashboard -config dashboard.yaml   # API on :9400
make ui-dev                                  # UI on http://localhost:5173, calling that API
```

Type-check with `cd ui && npm run typecheck`.
