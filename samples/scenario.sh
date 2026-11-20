#!/usr/bin/env sh
# Plays a scenario from samples/scenarios/<name>/. Each file is posted to the
# source it is named after, so the source's parser applies:
#   broker-events.ndjson -> POST /ingest/broker-events/batch (one event per line)
#   alertmanager.json    -> POST /ingest/alertmanager        (one event)
#
#   ./samples/scenario.sh counterparty-outage
#   ./samples/scenario.sh recovery
#   ./samples/scenario.sh pricing-burst
set -eu
URL="${KARDAN_URL:-http://localhost:8080}"
ROOT="$(cd "$(dirname "$0")" && pwd)/scenarios"
NAME="${1:-}"
if [ -z "$NAME" ] || [ ! -d "$ROOT/$NAME" ]; then
  echo "usage: $0 <scenario>"; echo "scenarios:"; ls "$ROOT" | sed 's/^/  /'; exit 1
fi
for f in "$ROOT/$NAME"/*; do
  src=$(basename "$f"); src=${src%.*}
  case "$f" in
    *.json|*.xml) path="/ingest/$src" ;;
    *)            path="/ingest/$src/batch" ;;
  esac
  printf '%-22s -> ' "$src"
  curl -sS -X POST --data-binary "@$f" "$URL$path"
  echo
done
