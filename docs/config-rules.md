[← Documentation](../README.md#documentation) · Previous: [agent.yaml](config-agent.md) · Next: [dashboard.yaml reference →](config-dashboard.md)

# rules.yaml reference

`rules.yaml` says what to watch and where to send alerts. It has two lists:
- **`webhooks`**: where alerts go
- **`rules`**: which lines, or missing lines, to alert on

How to write good rules: [Writing rules](rules.md).

**Where the file lives:**
- **On its own,** the agent reads it from `rules_file` in [agent.yaml](config-agent.md).
- **In a dashboard group,** the group's rules (edited in the UI or synced from git) are written there.

**Changing it:** changes apply without a restart, and an invalid version is rejected while the previous rules keep running. Check a file with `grepalert-agent -check rules.yaml`.

**Environment variables:** `${VAR}` is replaced with an environment variable **of the agent**, so webhook URLs and tokens stay out of the file and out of git. Only the `${VAR}` form is replaced here; a lone `$` stays as it is.

## Complete example

Every setting ([examples/rules.yaml](../examples/rules.yaml)):

```yaml
# grepalert rules: what to watch, and where to send alerts.
# Reference: docs/config-rules.md · Guide: docs/rules.md
#
# The agent applies changes to this file without a restart. Check a file
# first with: grepalert-agent -check rules.yaml
#
# ${VAR} is replaced with an environment variable OF THE AGENT, so URLs and
# tokens stay out of this file (and out of git).

webhooks:
  # Every setting of a webhook. Without a template, the whole alert is
  # POSTed as JSON (see docs/rules.md#webhook-payload).
  - name: ops
    url: ${OPS_WEBHOOK_URL}
    method: POST                     # default POST
    headers:                         # optional
      Authorization: Bearer ${OPS_WEBHOOK_TOKEN}
    timeout: 10s                     # default 10s

  # Slack, Mattermost, Google Chat: build the body with a Go template.
  - name: slack
    url: ${SLACK_WEBHOOK_URL}
    template: '{"text": {{ json .Text }}}'

  # Microsoft Teams (incoming webhook / workflow)
  - name: teams
    url: ${TEAMS_WEBHOOK_URL}
    template: '{"text": {{ json .Text }}}'

  # Discord: messages are limited to 2000 characters.
  - name: discord
    url: ${DISCORD_WEBHOOK_URL}
    template: '{"content": {{ json (trunc 1900 .Text) }}}'

rules:
  # Every setting of a matching rule.
  - name: nginx-5xx                          # required, unique
    description: Upstream errors served to clients
    files:                                   # files, or globs re-scanned for new files
      - /var/log/nginx/access.log
    pattern: '" (?P<status>5\d\d) '          # Go RE2 regexp; (?P<name>...) captures a value
    exclude: 'GET /healthz'                  # optional: lines matching this never alert
    severity: critical                       # optional free text: critical, warning, info...
    labels: {team: web}                      # optional, passed to webhooks
    webhooks: [slack]                        # one or more webhooks from above
    suppress:                                # optional: limit how often it alerts
      window: 5m                             # per window of this length...
      max_alerts: 1                          # ...at most this many alerts (default 1)
      group_by: status                       # ...for each distinct captured value
      summary: true                          # then one "N more were held back" summary (default true)
    # disabled: true                         # keep the rule, but don't run it

  # Your own message, built from the values the pattern captured.
  - name: datasource-suspended
    files: [/var/log/myapp/app.log]
    pattern: 'Datasource (?P<datasource>\S+) is suspended because (?P<reason>.+)'
    severity: critical
    webhooks: [ops]
    suppress: {window: 10m, group_by: datasource}

  # Globs: new files are picked up within rescan_interval.
  - name: app-exceptions
    files: ["/var/log/myapp/*.log"]
    pattern: '(?i)(exception|panic|fatal)'
    severity: warning
    webhooks: [ops, slack]
    suppress: {window: 10m, max_alerts: 3}

  # No suppress block: every matching line alerts.
  - name: disk-full
    files: [/var/log/syslog]
    pattern: 'No space left on device'
    severity: critical
    webhooks: [ops]

  # Absence rule: alerts when an expected line has NOT appeared for a while
  # (a hung app or a stopped job writes nothing, so error rules can't catch
  # it). It alerts once, then once more when the line is back ("recovered").
  - name: no-heartbeat
    files: [/var/log/myapp/app.log]
    absent: 'heartbeat OK'                   # instead of pattern
    for: 10m
    severity: critical
    webhooks: [ops]

  # Kubernetes: the logs of the pods a label selector picks, instead of files
  # (see docs/kubernetes.md).
  - name: checkout-errors
    kubernetes:
      namespace: shop                        # or "*" for every namespace
      selector: app=checkout                 # a label selector; empty = every pod
      container: app                         # optional; default every container
    pattern: 'ERROR (?P<code>\w+)'
    severity: critical
    webhooks: [slack]
    suppress: {window: 5m, group_by: code}
```

## Webhooks

| Setting | Default | Meaning |
|---|---|---|
| `name` | required | Unique name that rules refer to. |
| `url` | required | An `http://` or `https://` URL, usually `${SOME_WEBHOOK_URL}`. |
| `method` | `POST` | HTTP method. |
| `headers` | none | Extra request headers, e.g. `Authorization: Bearer ${TOKEN}`. Values may use `${VAR}`. |
| `template` | none | A Go template for the request body. Without one, the whole alert is sent as JSON. See [webhook payload](rules.md#webhook-payload) for the fields and helpers. |
| `timeout` | `10s` | How long one delivery may take. |

## Rules

| Setting | Default | Meaning |
|---|---|---|
| `name` | required | Unique name, shown in alerts. |
| `description` | none | Free text, passed to webhooks as `.Description`. |
| `files` | — | Files to watch. Globs such as `/var/log/app/*.log` are re-scanned every `rescan_interval`, so new files are picked up. A rule needs `files` **or** `kubernetes`. |
| `kubernetes` | — | Read the logs of the pods a label selector picks instead of files: `namespace` (`"*"` for every namespace), `selector` (a label selector; empty = every pod), optional `container`. See [Kubernetes](kubernetes.md). |
| `pattern` | — | A Go [RE2](https://github.com/google/re2/wiki/Syntax) regular expression; a line that matches alerts. `(?P<name>...)` captures part of the line as `.Groups.name`. A rule needs `pattern` **or** `absent`. |
| `exclude` | none | Lines that also match this never alert, e.g. `GET /healthz`. |
| `absent` | — | Instead of `pattern`: alert when **no** line matching this has appeared for `for`, and again ("recovered") when one does. |
| `for` | — | With `absent`: how long the line may be missing, e.g. `10m`. Required with `absent`. |
| `severity` | none | Free text, e.g. `critical`, `warning` or `info`. Shown in alert texts and passed to webhooks. |
| `labels` | none | Key/value pairs passed to webhooks, e.g. `team: payments`. |
| `webhooks` | required | Names of one or more webhooks from the `webhooks` list. |
| `suppress` | none | Limits how often the rule alerts (below). Without it, every matching line alerts. Not for absence rules. |
| `disabled` | `false` | Keep the rule in the file, but don't run it. |

## Suppression

| Setting | Default | Meaning |
|---|---|---|
| `suppress.window` | required | Length of a suppression window, e.g. `5m`. |
| `suppress.max_alerts` | `1` | Alerts allowed per window; matches after that are counted, not sent. |
| `suppress.group_by` | none | A named capture of the pattern. Each distinct value gets its own window, so one error code doesn't hide another. |
| `suppress.summary` | `true` | When a window closes after holding matches back, send one "N more matches were suppressed" alert. |

## What is checked

A rules file is rejected, with every problem listed, when:
- a rule or webhook has no `name`, or a name is used twice
- a webhook `url` is not an absolute `http(s)` URL, or its `template` doesn't parse
- a rule has neither `files` nor `kubernetes`, or both
- a rule has neither `pattern` nor `absent`, or both; or `absent` without `for`; or `absent` with `suppress`
- a `pattern` or `exclude` is not a valid regular expression
- `suppress.group_by` names a capture the pattern doesn't have
- a rule names a webhook that isn't in the list
- a Kubernetes namespace, selector or container is malformed

Local rules (`rules.local.yaml`) use the same format. They may use the webhooks of the main rules, but may not reuse the names of its rules or webhooks.
