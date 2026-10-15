[← Documentation](../README.md#documentation) · Previous: [Writing rules](rules.md) · Next: [Running agents →](agents.md)

# Rule cookbook

Complete rules for real log formats, from a threshold in a regular expression to Opsgenie and Slack Block Kit payloads. Each recipe is a file in [examples/cookbook/](../examples/cookbook/) you can copy, and each one is tested: the sample lines at its top go through the real engine, and the webhook bodies it sends must be the ones shown.

In each recipe:

- `in:` is a log line, in the order the agent reads them.
- `out:` is a webhook body the agent sends for them, in order. Lines without an `out:` do not alert. Summaries come last, when their window is over.

Try any of them in the dashboard's **Tools → Rule playground**: paste the rules and the `in:` lines, and it shows the same bodies.

## Recipes

- [Slow 5xx responses from an access log](#slow-5xx-responses-from-an-access-log)
- [Numeric thresholds](#numeric-thresholds)
- [Two severities from one kind of line](#two-severities-from-one-kind-of-line)
- [Structured JSON logs](#structured-json-logs)
- [Errors, minus the noise](#errors-minus-the-noise)
- [Java exceptions, not their stack frames](#java-exceptions-not-their-stack-frames)
- [PostgreSQL slow queries](#postgresql-slow-queries)
- [Failed SSH logins per address](#failed-ssh-logins-per-address)
- [One alert per tenant](#one-alert-per-tenant)
- [Opsgenie and Slack Block Kit](#opsgenie-and-slack-block-kit)
- [A nightly job that did not run](#a-nightly-job-that-did-not-run)
- [Kubernetes pods](#kubernetes-pods)
- [Pattern tips](#pattern-tips)

## Slow 5xx responses from an access log

Two conditions on one line, status **and** response time, in one pattern. The path stops at `?`, so `/api/cart?id=7` and `/api/cart?id=9` are the same path. `.*` skips the size, referer and user agent between the status and `rt=`; a response time of 2 seconds or more is a digit from 2 to 9, or two or more digits, before the dot.

[`examples/cookbook/web-5xx-slow.yaml`](../examples/cookbook/web-5xx-slow.yaml):

```yaml
# 5xx responses that were also slow: status AND response time in one pattern.
#   in:  10.0.0.7 - - [15/Oct/2026:10:00:01 +0000] "POST /api/checkout HTTP/1.1" 502 157 "-" "curl/8.5" rt=3.412
#   in:  10.0.0.8 - - [15/Oct/2026:10:00:02 +0000] "POST /api/checkout HTTP/1.1" 502 157 "-" "curl/8.5" rt=0.120
#   in:  10.0.0.9 - - [15/Oct/2026:10:00:03 +0000] "GET /api/cart?id=7 HTTP/1.1" 200 812 "-" "Mozilla/5.0" rt=4.950
#   in:  10.0.0.4 - - [15/Oct/2026:10:00:04 +0000] "GET /api/cart?id=9 HTTP/1.1" 504 0 "-" "Mozilla/5.0" rt=30.001
#   out: {"text": "502 POST /api/checkout took 3.412s"}
#   out: {"text": "504 GET /api/cart took 30.001s"}
webhooks:
  - name: chat
    url: ${CHAT_WEBHOOK_URL}
    template: '{"text": {{ json (printf "%s %s %s took %ss" .Groups.status .Groups.method .Groups.path .Groups.rt) }}}'

rules:
  - name: slow-5xx
    files: [/var/log/nginx/access.log]
    # method and path (without the query string), a 5xx status, and a response
    # time of 2 seconds or more: [2-9].x or two or more digits before the dot.
    pattern: '"(?P<method>[A-Z]+) (?P<path>/[^ ?"]*)[^"]*" (?P<status>5\d\d) .* rt=(?P<rt>(?:[2-9]|\d{2,})\.\d+)'
    severity: critical
    webhooks: [chat]
    suppress: {window: 5m, group_by: path}   # one alert per endpoint per 5 minutes
```

## Numeric thresholds

A regular expression compares text, not numbers, so spell out the digits of the range you want: 500 and more is `[5-9]\d\d` (500 to 999) or `[1-9]\d{3,}` (1000 and more). `499` does not match, `89%` does not match. Two rules, each with its own message template.

[`examples/cookbook/thresholds.yaml`](../examples/cookbook/thresholds.yaml):

```yaml
# Numeric thresholds with a regular expression: spell out the digits of the
# numbers you want. Requests of 500 ms or more, and disks at 90% or fuller.
#   in:  GET /api/search took=499ms user=41
#   in:  GET /api/search took=500ms user=42
#   in:  POST /api/report took=12034ms user=7
#   in:  disk /data is 89% full
#   in:  disk /data is 93% full
#   in:  disk /var/lib/postgresql is 100% full
#   out: {"text": "slow: GET /api/search took 500 ms"}
#   out: {"text": "slow: POST /api/report took 12034 ms"}
#   out: {"text": "disk /data is at 93%"}
#   out: {"text": "disk /var/lib/postgresql is at 100%"}
webhooks:
  - name: slow
    url: ${CHAT_WEBHOOK_URL}
    template: '{"text": {{ json (printf "slow: %s took %s ms" .Groups.request .Groups.ms) }}}'
  - name: disk
    url: ${CHAT_WEBHOOK_URL}
    template: '{"text": {{ json (printf "disk %s is at %s%%" .Groups.mount .Groups.pct) }}}'

rules:
  - name: slow-requests
    files: [/var/log/app/app.log]
    # 500-999 is [5-9]\d\d; 1000 and more is a non-zero digit and 3+ more digits.
    pattern: '(?P<request>(?:GET|POST|PUT|DELETE) \S+) took=(?P<ms>[5-9]\d\d|[1-9]\d{3,})ms'
    severity: warning
    webhooks: [slow]

  - name: disk-almost-full
    files: [/var/log/app/app.log]
    # 90-99 is 9\d; 100 is spelled out.
    pattern: 'disk (?P<mount>\S+) is (?P<pct>9\d|100)% full'
    severity: critical
    webhooks: [disk]
```

## Two severities from one kind of line

Two rules over the same line, each covering its own range of numbers, so a certificate is either a warning or critical, never both. `(upper .Severity)` puts the rule's severity in the message; `days?` matches both *1 day* and *3 days*.

[`examples/cookbook/certificate-expiry.yaml`](../examples/cookbook/certificate-expiry.yaml):

```yaml
# Certificates about to expire, with two severities from the same kind of
# line: a warning from 29 days down to 7, critical under 7. Each rule matches
# its own range of numbers.
#   in:  cert-check: certificate api.example.com expires in 45 days
#   in:  cert-check: certificate shop.example.com expires in 21 days
#   in:  cert-check: certificate pay.example.com expires in 3 days
#   in:  cert-check: certificate old.example.com expires in 1 day
#   out: {"text": "WARNING: shop.example.com expires in 21 days"}
#   out: {"text": "CRITICAL: pay.example.com expires in 3 days"}
#   out: {"text": "CRITICAL: old.example.com expires in 1 day"}
webhooks:
  - name: chat
    url: ${CHAT_WEBHOOK_URL}
    template: '{"text": {{ json (printf "%s: %s expires in %s" (upper .Severity) .Groups.cn .Groups.left) }}}'

rules:
  - name: certificate-expires-soon
    files: [/var/log/cert-check.log]
    pattern: 'certificate (?P<cn>\S+) expires in (?P<left>(?:[7-9]|[12]\d) days)'   # 7 to 29 days
    severity: warning
    webhooks: [chat]

  - name: certificate-expires-now
    files: [/var/log/cert-check.log]
    pattern: 'certificate (?P<cn>\S+) expires in (?P<left>[0-6] days?)'   # 0 to 6 days
    severity: critical
    webhooks: [chat]
```

## Structured JSON logs

Match the fields of a JSON line in the order the logger writes them. A message may contain escaped quotes, so it is matched as "any character but a quote or backslash, or a backslash and the character after it": `(?:[^"\\]|\\.)*`. The `json` helper escapes the captured text again for the webhook body.

If your logger does not keep a fixed field order, match each field you need with its own rule, or put the level in the pattern and the rest in the message (`.Line`).

[`examples/cookbook/json-logs.yaml`](../examples/cookbook/json-logs.yaml):

```yaml
# Structured JSON logs: match the level, pull out fields, and send a short message.
#   in:  {"time":"2026-10-15T10:00:01Z","level":"error","service":"billing","msg":"invoice 4411 failed: card declined","trace_id":"9f2c"}
#   in:  {"time":"2026-10-15T10:00:02Z","level":"info","service":"billing","msg":"invoice 4412 paid","trace_id":"77ab"}
#   in:  {"time":"2026-10-15T10:00:03Z","level":"fatal","service":"ledger","msg":"cannot open \"ledger.db\": disk I/O error","trace_id":"c0de"}
#   out: {"text": "[billing] invoice 4411 failed: card declined (trace 9f2c)"}
#   out: {"text": "[ledger] cannot open \\\"ledger.db\\\": disk I/O error (trace c0de)"}
webhooks:
  - name: chat
    url: ${CHAT_WEBHOOK_URL}
    template: '{"text": {{ json (printf "[%s] %s (trace %s)" .Groups.service .Groups.msg .Groups.trace) }}}'

rules:
  - name: json-errors
    files: ["/var/log/services/*.json"]
    # Fields are matched in the order the logger writes them. A message may
    # contain escaped quotes, so it is "any non-quote, or a backslash pair".
    pattern: '"level":"(?:error|fatal)","service":"(?P<service>[^"]+)","msg":"(?P<msg>(?:[^"\\]|\\.)*)","trace_id":"(?P<trace>[^"]*)"'
    severity: critical
    webhooks: [chat]
```

## Errors, minus the noise

Go's regular expressions (RE2) have no lookahead, so "error, but not /healthz" cannot be one pattern. `exclude` does it: a line that matches it never alerts. `\b` keeps `errors_total` from counting as an error, and `(?i)` ignores case in both.

[`examples/cookbook/exclude-noise.yaml`](../examples/cookbook/exclude-noise.yaml):

```yaml
# Errors, minus the noise. Go's regular expressions (RE2) have no lookahead
# like (?!...); use exclude instead: a line that matches exclude never alerts.
#   in:  2026-10-15 10:00:01 ERROR payment provider returned 503
#   in:  2026-10-15 10:00:02 ERROR GET /healthz: upstream not ready
#   in:  2026-10-15 10:00:03 WARN read tcp 10.0.0.5:443: ECONNRESET (client disconnected)
#   in:  2026-10-15 10:00:04 Exception in worker 3: NullPointerException
#   in:  2026-10-15 10:00:05 INFO errors_total=0
#   in:  2026-10-15 10:00:06 ERROR deprecated config key "timeout_ms", use "timeout"
#   out: {"text": "2026-10-15 10:00:01 ERROR payment provider returned 503"}
#   out: {"text": "2026-10-15 10:00:04 Exception in worker 3: NullPointerException"}
webhooks:
  - name: chat
    url: ${CHAT_WEBHOOK_URL}
    template: '{"text": {{ json .Line }}}'

rules:
  - name: errors-without-noise
    files: ["/var/log/app/*.log"]
    # \b keeps "errors_total" from counting as "error"; (?i) ignores case.
    pattern: '(?i)\b(?:error|exception|fatal)\b'
    # Known noise: health checks, clients that went away, deprecation notices.
    exclude: '(?i)/healthz|/readyz|ECONNRESET|client disconnected|deprecated'
    severity: critical
    webhooks: [chat]
```

## Java exceptions, not their stack frames

`^` anchors the pattern at the start of the line, so the indented `at ...` frames and the `Caused by:` lines under an exception never match; only the line that names it does. The package is matched but not captured, and the message after `: ` is optional. `group_by: class` alerts once per exception class, with no summary.

[`examples/cookbook/java-exceptions.yaml`](../examples/cookbook/java-exceptions.yaml):

```yaml
# Java exceptions: alert on the line that names the exception, not on the stack
# frames or "Caused by" lines below it.
#   in:  2026-10-15 10:00:01 ERROR [pool-3-thread-1] c.a.Billing - charge failed
#   in:  java.lang.IllegalStateException: Connection pool shut down
#   in:  	at com.acme.billing.Billing.charge(Billing.java:42)
#   in:  Caused by: java.net.SocketTimeoutException: Read timed out
#   in:  Exception in thread "main" java.lang.OutOfMemoryError: Java heap space
#   in:  java.lang.IllegalStateException: Connection pool shut down
#   out: {"text": "IllegalStateException: Connection pool shut down"}
#   out: {"text": "OutOfMemoryError: Java heap space"}
webhooks:
  - name: chat
    url: ${CHAT_WEBHOOK_URL}
    template: '{"text": {{ json (printf "%s: %s" .Groups.class .Groups.message) }}}'

rules:
  - name: java-exception
    files: ["/var/log/app/*.log"]
    # ^ anchors at the line start, so indented "at ..." frames and "Caused by:"
    # never match. The package (lower case, dotted) is matched but not kept.
    pattern: '^(?:Exception in thread "[^"]+" )?(?:[a-z_][\w$]*\.)+(?P<class>[A-Z][\w$]*(?:Exception|Error))(?:: (?P<message>.*))?$'
    severity: critical
    webhooks: [chat]
    suppress: {window: 10m, group_by: class, summary: false}   # the same exception at most every 10 minutes
```

## PostgreSQL slow queries

With `log_min_duration_statement` set, PostgreSQL logs the duration of slow statements. The pattern takes queries of a second or more, plain and prepared (`execute <name>:`) ones, and keeps at most 80 characters of the SQL with `.{1,80}`. `max_alerts: 5` allows five alerts per 10 minutes before the rest wait for the summary.

[`examples/cookbook/postgres-slow-queries.yaml`](../examples/cookbook/postgres-slow-queries.yaml):

```yaml
# PostgreSQL slow queries (log_min_duration_statement): queries of a second or
# more, with the start of the statement, at most 80 characters of it.
#   in:  2026-10-15 10:00:01.123 UTC [4242] LOG:  duration: 999.874 ms  statement: SELECT 1
#   in:  2026-10-15 10:00:02.456 UTC [4243] LOG:  duration: 2534.871 ms  statement: SELECT * FROM orders WHERE customer_id = 7 ORDER BY created_at DESC
#   in:  2026-10-15 10:00:03.789 UTC [4244] LOG:  duration: 61002.004 ms  execute S_1: UPDATE ledger SET balance = balance - $1 WHERE account_id = $2
#   out: {"text": "2534 ms: SELECT * FROM orders WHERE customer_id = 7 ORDER BY created_at DESC"}
#   out: {"text": "61002 ms: UPDATE ledger SET balance = balance - $1 WHERE account_id = $2"}
webhooks:
  - name: dba
    url: ${DBA_WEBHOOK_URL}
    template: '{"text": {{ json (printf "%s ms: %s" .Groups.ms .Groups.sql) }}}'

rules:
  - name: slow-query
    files: ["/var/log/postgresql/*.log"]
    # 1000 ms or more: a non-zero digit and 3+ more before the decimals.
    # Both plain statements and prepared ones ("execute <name>:") count.
    pattern: 'duration: (?P<ms>[1-9]\d{3,})\.\d+ ms\s+(?:statement|execute [^:]+): (?P<sql>.{1,80})'
    severity: warning
    webhooks: [dba]
    suppress: {window: 10m, max_alerts: 5}   # at most 5 per 10 minutes, then a summary
```

## Failed SSH logins per address

One alert per attacking address (`group_by: ip`), then one summary per address when the 15 minutes are over. The template checks `.Kind` to word the summary differently, using `.Suppressed`, the number of lines it held back. `(?:invalid user )?` makes the words optional without capturing them; the IP address is four dotted groups of 1 to 3 digits.

[`examples/cookbook/ssh-bruteforce.yaml`](../examples/cookbook/ssh-bruteforce.yaml):

```yaml
# Failed SSH logins: one alert per attacking address, and a summary of how many
# more attempts it made. The template words summaries differently.
#   in:  Oct 15 10:00:01 web-01 sshd[811]: Failed password for root from 203.0.113.9 port 50122 ssh2
#   in:  Oct 15 10:00:02 web-01 sshd[811]: Failed password for root from 203.0.113.9 port 50124 ssh2
#   in:  Oct 15 10:00:03 web-01 sshd[812]: Failed password for invalid user admin from 203.0.113.9 port 50126 ssh2
#   in:  Oct 15 10:00:04 web-01 sshd[813]: Failed publickey for deploy from 198.51.100.23 port 40022 ssh2
#   in:  Oct 15 10:00:05 web-01 sshd[814]: Accepted publickey for deploy from 198.51.100.23 port 40030 ssh2
#   out: {"text": "Failed SSH login as root from 203.0.113.9"}
#   out: {"text": "Failed SSH login as deploy from 198.51.100.23"}
#   out: {"text": "2 more failed SSH logins from 203.0.113.9 in 15 minutes"}
webhooks:
  - name: security
    url: ${SECURITY_WEBHOOK_URL}
    template: >-
      {"text": {{ if eq .Kind "summary" -}}
        {{ json (printf "%d more failed SSH logins from %s in 15 minutes" .Suppressed .Groups.ip) }}
      {{- else -}}
        {{ json (printf "Failed SSH login as %s from %s" .Groups.user .Groups.ip) }}
      {{- end }}}

rules:
  - name: ssh-failed-login
    files: [/var/log/auth.log]
    pattern: 'sshd\[\d+\]: Failed (?:password|publickey) for (?:invalid user )?(?P<user>\S+) from (?P<ip>\d{1,3}(?:\.\d{1,3}){3}) port \d+'
    severity: warning
    webhooks: [security]
    suppress: {window: 15m, group_by: ip}   # per address: the first attempt, then a count
```

## One alert per tenant

`group_by: tenant` gives every tenant its own suppression window, so a noisy tenant does not hide a quiet one. A summary carries the captured values of the last line it held back, so it can name the tenant and the last resource.

[`examples/cookbook/per-tenant.yaml`](../examples/cookbook/per-tenant.yaml):

```yaml
# A multi-tenant service: the same problem for different tenants alerts
# separately, at most once an hour per tenant, naming the tenant. When the
# hour is over, one summary says how many more times it happened.
#   in:  2026-10-15 10:00:01 WARN tenant=acme-corp plan=pro quota exceeded: api_calls 100000/100000
#   in:  2026-10-15 10:00:02 WARN tenant=acme-corp plan=pro quota exceeded: api_calls 100000/100000
#   in:  2026-10-15 10:00:03 WARN tenant=globex plan=free quota exceeded: storage 5120/5120
#   in:  2026-10-15 10:00:04 WARN tenant=acme-corp plan=pro quota exceeded: storage 900/900
#   out: {"text": "acme-corp (pro) hit its api_calls quota"}
#   out: {"text": "globex (free) hit its storage quota"}
#   out: {"text": "acme-corp hit a quota again, 2 more times in the last hour (last: storage)"}
webhooks:
  - name: success-team
    url: ${CUSTOMER_SUCCESS_WEBHOOK_URL}
    template: >-
      {"text": {{ if eq .Kind "summary" -}}
        {{ json (printf "%s hit a quota again, %d more times in the last hour (last: %s)" .Groups.tenant .Suppressed .Groups.resource) }}
      {{- else -}}
        {{ json (printf "%s (%s) hit its %s quota" .Groups.tenant .Groups.plan .Groups.resource) }}
      {{- end }}}

rules:
  - name: quota-exceeded
    files: [/var/log/api/api.log]
    pattern: 'tenant=(?P<tenant>[a-z0-9-]+) plan=(?P<plan>\w+) quota exceeded: (?P<resource>\w+)'
    severity: info
    webhooks: [success-team]
    suppress: {window: 1h, group_by: tenant}
```

## Opsgenie and Slack Block Kit

One rule, two receivers with very different payloads. Opsgenie needs its API key in a header and deduplicates on `alias`; built from the rule and the captured reason, repeated failures for the same reason update one Opsgenie alert. The Slack template builds Block Kit fields; `json` escapes every value and turns the `\n` from `printf` into a line break in Slack.

Multi-line templates are fine: the body is sent exactly as the template renders it.

[`examples/cookbook/opsgenie-and-slack-blocks.yaml`](../examples/cookbook/opsgenie-and-slack-blocks.yaml):

```yaml
# Rich payloads: an Opsgenie alert (deduplicated by rule and reason, with the
# API key in a header) and a Slack message laid out with Block Kit fields.
#   in:  2026-10-15 10:00:01 ERROR order=88123 payment failed: gateway timeout after 30s
#   out: {"message": "payment-failed: gateway timeout after 30s", "alias": "payment-failed/gateway timeout after 30s", "priority": "P1", "description": "2026-10-15 10:00:01 ERROR order=88123 payment failed: gateway timeout after 30s", "tags": ["grepalert", "payment-failed"], "details": {"host": "playground", "order": "88123"}}
#   out: {"blocks": [{"type": "header", "text": {"type": "plain_text", "text": "payment-failed (critical)"}}, {"type": "section", "fields": [{"type": "mrkdwn", "text": "*Order*\n88123"}, {"type": "mrkdwn", "text": "*Reason*\ngateway timeout after 30s"}]}]}
webhooks:
  - name: opsgenie
    url: https://api.opsgenie.com/v2/alerts
    headers:
      Authorization: GenieKey ${OPSGENIE_API_KEY}
    template: |
      {
        "message": {{ json (printf "%s: %s" .Rule .Groups.reason) }},
        "alias": {{ json (printf "%s/%s" .Rule .Groups.reason) }},
        "priority": {{ if eq .Severity "critical" }}"P1"{{ else }}"P3"{{ end }},
        "description": {{ json .Line }},
        "tags": ["grepalert", {{ json .Rule }}],
        "details": {"host": {{ json .Host }}, "order": {{ json .Groups.order }}}
      }

  - name: slack-blocks
    url: ${SLACK_WEBHOOK_URL}
    template: |
      {"blocks": [
        {"type": "header", "text": {"type": "plain_text", "text": {{ json (printf "%s (%s)" .Rule .Severity) }}}},
        {"type": "section", "fields": [
          {"type": "mrkdwn", "text": {{ json (printf "*Order*\n%s" .Groups.order) }}},
          {"type": "mrkdwn", "text": {{ json (printf "*Reason*\n%s" .Groups.reason) }}}
        ]}
      ]}

rules:
  - name: payment-failed
    files: [/var/log/shop/app.log]
    pattern: 'order=(?P<order>\d+) payment failed: (?P<reason>.+)'
    severity: critical
    webhooks: [opsgenie, slack-blocks]
    suppress: {window: 10m, group_by: reason}
```

## A nightly job that did not run

An absence rule alerts when its line has **not** appeared for `for:`, and once more (`.Kind` is `recovered`) when it shows up again. 26 hours gives a nightly job two hours of slack.

[`examples/cookbook/nightly-backup.yaml`](../examples/cookbook/nightly-backup.yaml):

```yaml
# A job that must report success every night: alert when the line has NOT
# appeared for 26 hours (a day plus some slack), and again when it is back.
# Absence rules depend on time passing, so this recipe has no in/out sample.
webhooks:
  - name: ops
    url: ${OPS_WEBHOOK_URL}
    template: >-
      {"text": {{ if eq .Kind "recovered" -}}
        {{ json (printf "Backups are running again: %s" .Line) }}
      {{- else -}}
        {{ json "No successful backup in the last 26 hours" }}
      {{- end }}}

rules:
  - name: backup-missing
    files: [/var/log/backup.log]
    absent: 'backup of \w+ completed successfully'
    for: 26h
    severity: critical
    webhooks: [ops]
```

## Kubernetes pods

Any of the recipes above works for pod logs: replace `files` with `kubernetes`. The selector is a Kubernetes label selector, so `track!=canary` leaves the canary out. See [Kubernetes](kubernetes.md).

[`examples/cookbook/kubernetes-pods.yaml`](../examples/cookbook/kubernetes-pods.yaml):

```yaml
# The same kind of rule for Kubernetes pods: the "app" container of every pod
# labelled app=checkout, except the canary, in namespace shop. Each decline
# code alerts once per 5 minutes, without a summary afterwards.
#   in:  2026-10-15T10:00:01Z ERROR [req 7f3a] payment declined code=CARD_EXPIRED
#   in:  2026-10-15T10:00:02Z ERROR [req 7f3b] payment declined code=CARD_EXPIRED
#   in:  2026-10-15T10:00:03Z ERROR [req 7f3c] payment declined code=INSUFFICIENT_FUNDS
#   out: {"text": "checkout: payment declined (CARD_EXPIRED)"}
#   out: {"text": "checkout: payment declined (INSUFFICIENT_FUNDS)"}
webhooks:
  - name: chat
    url: ${CHAT_WEBHOOK_URL}
    template: '{"text": {{ json (printf "checkout: payment declined (%s)" .Groups.code) }}}'

rules:
  - name: checkout-declines
    kubernetes:
      namespace: shop
      selector: app=checkout,track!=canary   # any Kubernetes label selector
      container: app
    pattern: 'payment declined code=(?P<code>[A-Z_]+)'
    severity: warning
    webhooks: [chat]
    suppress: {window: 5m, group_by: code, summary: false}
```

## Pattern tips

- **Use single quotes in YAML.** Inside `'...'` a backslash is just a backslash, so `\d` and `\.` reach the regular
  expression as written. In double quotes YAML reads the backslashes itself. A single quote inside is written `''`.
- **No lookahead, lookbehind or backreferences.** grepalert uses Go's RE2, which always runs in time proportional to
  the line, so one bad pattern cannot stall the agent. Use `exclude` for "but not".
- **Anchor when you can.** `^` and `$` stop a pattern from matching in the middle of a line you did not mean,
  such as a stack frame or a quoted message.
- **`(?i)` ignores case** for the whole pattern; `(?i:error)` only for a part.
- **`(?:...)` groups without capturing;** `(?P<name>...)` captures into `.Groups.name`.
- **Numbers are text.** Spell out the range with digit classes, as in [Numeric thresholds](#numeric-thresholds).
- **Keep `.*` short.** `[^"]*` or `\S+` say what may be skipped, and stop where you expect them to.
- **Suppression is not a count threshold.** `window` and `max_alerts` limit how many alerts are sent;
  they do not wait for N matches before the first one. The first match always alerts.
- **Test before you ship:** `grepalert-agent -check rules.yaml` for validity, the rule playground for what
  matches and what is sent. See [Testing rules](rules.md#test-before-you-deploy).
