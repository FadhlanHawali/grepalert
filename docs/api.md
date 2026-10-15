[← Documentation](../README.md#documentation) · Previous: [Sign-in, roles and security](security.md) · Next: [Memory and performance →](performance.md)

# Agent API

| Method & path | Purpose |
|---|---|
| `GET /healthz` | Liveness check (no auth). |
| `GET /api/v1/status` | Files, offsets, per-rule counters, webhook stats, memory. |
| `GET /api/v1/rules` | The active rules YAML. The `X-Rules-Hash` header identifies the version. |
| `PUT /api/v1/rules` | Validate, persist and apply new rules. |
| `POST /api/v1/rules/validate` | Validate rules without applying them. |
| `POST /api/v1/rules/dryrun?window=1h` | Replay the last `window` (up to 3h) of the logs through the posted rules and the current ones; returns per-rule alert counts and examples. Nothing is sent. |
| `GET /api/v1/rules/local` | This machine's local rules (JSON: `rules`, `hash`, `rejected`). |
| `PUT /api/v1/rules/local` | Replace the local rules (validated against the current rules); an empty body removes them. |
| `GET /api/v1/events?limit=N` | Recent alerts, rule changes and errors. |
| `POST /api/v1/test` | `{"line": "..."}`: lists which rules would match that line. |
