#!/bin/sh
# Demo log generator: writes an application log and an nginx-style access log,
# with occasional errors, and rotates the app log every ~5 minutes.
set -eu
cd /var/log/demo
i=0
while true; do
  i=$((i + 1))
  ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  r=$((RANDOM % 100))

  if [ "$r" -lt 4 ]; then
    echo "$ts ERROR code=DB_TIMEOUT msg=\"query exceeded 5s\" req=$i" >> app.log
  elif [ "$r" -lt 6 ]; then
    echo "$ts ERROR code=PAYMENT_DECLINED msg=\"gateway returned 402\" req=$i" >> app.log
  elif [ "$r" -eq 99 ] && [ $((RANDOM % 4)) -eq 0 ]; then
    echo "$ts FATAL panic: runtime error: invalid memory address req=$i" >> app.log
  else
    echo "$ts INFO request served path=/api/items/$((RANDOM % 500)) took=$((RANDOM % 90 + 3))ms req=$i" >> app.log
  fi

  status=200
  if [ $((RANDOM % 100)) -lt 3 ]; then status=$((502 + RANDOM % 3)); fi
  echo "10.0.0.$((RANDOM % 254 + 1)) - - [$ts] \"GET /api/items HTTP/1.1\" $status $((RANDOM % 4000))" >> access.log

  # Rotate like logrotate (rename + create) every 600 iterations (~5 min).
  if [ $((i % 600)) -eq 0 ]; then
    mv app.log app.log.1
    echo "$ts INFO log rotated" >> app.log
  fi
  sleep 0.5
done
