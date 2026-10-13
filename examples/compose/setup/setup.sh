#!/bin/sh
# One-shot demo setup through the dashboard API: the app-servers group (rules
# from the stand-in git repo, staged rollout on) and the ops webhook. Safe to
# run again: existing items are left alone.
set -u
API=http://dashboard:9400
AUTH="admin:${GREPALERT_DASHBOARD_PASSWORD}"
H='X-Requested-With: grepalert'

echo "waiting for the dashboard..."
until curl -sf "$API/api/auth/config" >/dev/null; do sleep 2; done

call() { # method path body
  code=$(curl -s -o /tmp/out -w '%{http_code}' -u "$AUTH" -H "$H" -H 'Content-Type: application/json' -X "$1" "$API$2" -d "$3")
  echo "$1 $2 -> $code $(head -c 160 /tmp/out)"
}

call POST /api/groups '{
  "name": "app-servers",
  "description": "Demo API servers; rules come from git-repo/rules.yaml",
  "source": "git",
  "git": {"provider": "url", "path": "http://webhook-sink:9599/repo/rules.yaml", "interval": "1m"},
  "selector": {"role": "app"},
  "fleet": {"wait": "5s", "window": "5m"},
  "rollout": {"enabled": true, "stages": ["1", "50%", "100%"], "soak": "1m", "max_increase": 3, "min_alerts": 10}
}'
call PUT /api/settings '{"ops_webhook":{"url":"http://webhook-sink:9599/hook/ops","template":"{\"text\": {{ json .Text }}}"},"agent_down_after":"1m"}'
echo "setup done"
