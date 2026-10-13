#!/bin/sh
# One-shot setup through the dashboard API, as the break-glass root admin:
# groups (agents join them by label), role bindings for Keycloak groups and
# the ops webhook. Safe to run again: existing items are left alone.
set -u
API=http://dashboard:9400
AUTH="root:${GREPALERT_ROOT_PASSWORD}"
H='X-Requested-With: grepalert'

echo "waiting for the dashboard..."
until curl -sf "$API/api/auth/config" >/dev/null; do sleep 2; done

call() { # method path body
  code=$(curl -s -o /tmp/out -w '%{http_code}' -u "$AUTH" -H "$H" -H 'Content-Type: application/json' -X "$1" "$API$2" -d "$3")
  echo "$1 $2 -> $code $(head -c 160 /tmp/out)"
}

call POST /api/groups @/setup/group-db.json
call POST /api/groups @/setup/group-web.json
call POST /api/access '{"subject":"group:db-team","role":"editor","group":"db"}'
call POST /api/access '{"subject":"group:web-team","role":"editor","group":"web"}'
call POST /api/access '{"subject":"user:carol@example.com","role":"viewer","group":"db"}'
call PUT  /api/settings '{"ops_webhook":{"url":"http://webhook-sink:9599/hook/ops","template":"{\"text\": {{ json .Text }}}"},"agent_down_after":"1m"}'
echo "setup done"
