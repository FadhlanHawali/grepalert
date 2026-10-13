#!/bin/sh
# Demo logs: a database log (errors + a regular checkpoint line, watched by an
# absence rule) and a web access log (some 5xx responses).
set -eu
cd /var/log/demo
i=0
while true; do
  i=$((i + 1))
  ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  case $((RANDOM % 40)) in
    0) echo "$ts ERROR DEADLOCK detected on table orders (req $i)" >> db.log ;;
    1) echo "$ts ERROR REPLICATION_LAG replica-2 is 34s behind" >> db.log ;;
    *) echo "$ts INFO query ok took=$((RANDOM % 50))ms" >> db.log ;;
  esac
  [ $((i % 5)) -eq 0 ] && echo "$ts LOG checkpoint complete: wrote $((RANDOM % 900)) buffers" >> db.log
  status=200
  [ $((RANDOM % 30)) -eq 0 ] && status=$((500 + RANDOM % 4))
  echo "10.0.0.$((RANDOM % 250 + 1)) - - [$ts] \"GET /shop/cart HTTP/1.1\" $status $((RANDOM % 5000))" >> web.log
  sleep 1
done
