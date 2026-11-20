#!/usr/bin/env sh
# Sends the Appendix A sample events to a running Kardan instance.
#   ./samples/send.sh                 # http://localhost:8080
#   KARDAN_URL=http://host:8080 ./samples/send.sh
set -eu
URL="${KARDAN_URL:-http://localhost:8080}"
DIR="$(cd "$(dirname "$0")" && pwd)/events"

post() { # source file [path-suffix]
  printf '%-28s -> ' "$2"
  curl -sS -X POST -H 'Content-Type: application/json' --data-binary "@$DIR/$2" "$URL/ingest/$1${3:-}"
  echo
}

post workflow-engine  workflow-failed.json
post alertmanager     metric-breach.json
post trade-gateway    trade-rejected.json
post pricing-logs     pricing-warn.ndjson /batch

echo "Done. Open $URL (alerts) and http://localhost:8025 (Mailpit)."
