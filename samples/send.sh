#!/usr/bin/env sh
# Sends the sample events through every source type:
#   HTTP webhooks -> alertmanager (Prometheus metric breach), swift-gateway (ISO 20022 pacs.002 XML)
#   Kafka topics  -> workflow.events, trade.events              (source "broker-events")
#   Log files     -> samples/logs/pricing-engine.log, fix-gateway.log (sources "pricing-logs", "fix-gateway")
#
#   ./samples/send.sh                         # against http://localhost:8080
#   KARDAN_URL=http://host:8080 ./samples/send.sh
set -eu
URL="${KARDAN_URL:-http://localhost:8080}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

say() { printf '%-44s' "$1"; }

say "HTTP  alertmanager  metric-breach.json"
curl -sS -X POST -H 'Content-Type: application/json' --data-binary @samples/events/metric-breach.json "$URL/ingest/alertmanager"; echo
say "HTTP  swift-gateway pacs002-rejected.xml"
curl -sS -X POST -H 'Content-Type: application/xml' --data-binary @samples/events/pacs002-rejected.xml "$URL/ingest/swift-gateway"; echo

for topic in workflow.events trade.events; do
  say "Kafka $topic"
  if docker compose exec -T redpanda rpk topic produce "$topic" < "samples/kafka/$topic.ndjson" >/dev/null 2>&1; then
    echo "produced $(wc -l < "samples/kafka/$topic.ndjson" | tr -d ' ') record(s)"
  else
    # No compose stack (e.g. running the binary locally): use the HTTP batch endpoint instead.
    curl -sS -X POST --data-binary "@samples/kafka/$topic.ndjson" "$URL/ingest/broker-events/batch"; echo " (via HTTP)"
  fi
done

mkdir -p samples/logs
say "File  samples/logs/pricing-engine.log"
cat samples/lines/pricing.log >> samples/logs/pricing-engine.log && echo "appended $(wc -l < samples/lines/pricing.log | tr -d ' ') lines"
say "File  samples/logs/fix-gateway.log"
cat samples/lines/fix.log >> samples/logs/fix-gateway.log && echo "appended $(wc -l < samples/lines/fix.log | tr -d ' ') lines"

echo
echo "Done. Alerts: $URL   Emails: http://localhost:8025   Schemas: $URL/schemas"
