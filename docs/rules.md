[← Documentation index](../README.md#documentation)

# Rules

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

See [examples/rules.yaml](../examples/rules.yaml) for more examples.

## Suppression

Without a `suppress` block, every matching line sends an alert. With one:

- Each window lets up to `max_alerts` alerts through (default 1). Matches after that are counted but not sent.
- `group_by` names a capture group in the pattern. Each distinct value gets its own window, so `E1` errors do not hide `E2` errors.
- When a window closes and some matches were held back, one summary alert is sent, for example "49 more matches were suppressed (E1). Last: …". Set `summary: false` to turn this off.

## Absence rules

Error rules can't catch an app that hangs or a cron job that stopped, because those write nothing. An absence rule alerts when an expected line has *not* appeared for a while:

```yaml
  - name: no-heartbeat
    files: ["/var/log/myapp/app.log"]
    absent: 'heartbeat OK'    # instead of pattern:
    for: 10m
    webhooks: [ops]
```

It alerts once after `for` has passed without a matching line, and once more ("recovered") when the line shows up again. A newly loaded rule gets its full `for` before it can fire.

## Webhook payload

With no `template`, the agent POSTs the alert as JSON:

```json
{"kind":"match","agent":"web-01","host":"web-01","rule":"nginx-5xx","severity":"critical",
 "labels":{"team":"web"},"file":"/var/log/nginx/access.log","line":"…","groups":{"status":"502"},
 "group_key":"502","time":"2026-10-12T20:41:37+07:00","suppressed":0,"text":"CRITICAL [web-01] nginx-5xx matched in …"}
```

`kind` is `match`, `summary`, `absent`, `recovered`, `fleet_summary`, `agent_down` or `agent_up`. Fleet alerts also carry `hosts`, `host_count` and `group`; `via` says whether the dashboard or the agent delivered it, and `id` lets receivers drop duplicates. A `template` is a Go `text/template` rendered with the same fields (`.Rule`, `.Line`, `.Groups.status`, `.Text`, …). Template helpers: `json` (a safely quoted JSON string), `upper`, `lower`, `title` (`critical` → `Critical`), `trunc N`.

**Sending only the parts of the line you need.** Name the parts in the pattern with `(?P<name>...)`; the template gets them as `.Groups.name`. For the line `Datasource DS1 is suspended because the pool is exhausted`:

```yaml
webhooks:
  - name: chat
    url: ${CHAT_WEBHOOK_URL}
    template: '{"text": {{ json (printf "%s, %s is suspended" (title .Severity) .Groups.datasource) }}}'

rules:
  - name: datasource-suspended
    files: [/var/log/app/app.log]
    pattern: 'Datasource (?P<datasource>\S+) is suspended because (?P<reason>.+)'
    severity: critical
    webhooks: [chat]
    suppress: {window: 10m, group_by: datasource}   # one alert per datasource per 10 minutes
```

The webhook receives `{"text": "Critical, DS1 is suspended"}`.
- **Summaries render the same way.** The "N more suppressed" summary carries the captured values of the last suppressed line, and so do fleet summaries, so the same template works for them too.
- **Try it first.** The dashboard's **Rule playground** (under *Tools*) takes rules and pasted log lines and shows the result as you type:
  - which line matched which rule
  - what each `(?P<name>...)` captured
  - whether each match alerts, is suppressed or is excluded
  - the exact body each webhook would receive, including suppression summaries

  It saves and sends nothing. You can start from the rules of any group.
- **Branching on the kind.** To word summaries differently, branch on the kind: `{{ if eq .Kind "summary" }}…{{ end }}`.
- **Other fields.** `.Groups.reason` is there too, as are `.Agent`, `.Host` and `.Time`.
