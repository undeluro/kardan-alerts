#!/usr/bin/env sh
# "A day on the rates desk": the demo story, one step at a time.
#
#   ./samples/rates-desk-day.sh            press Enter between steps
#   STEP_DELAY=8 ./samples/rates-desk-day.sh   unattended, 8 s between steps
#
# Keep three tabs open while it runs:
#   http://localhost:8080   Kardan (alert queue, incident, runbook panel)
#   http://localhost:8090   phone simulator (SMS, calls, replies)
#   http://localhost:8025   Mailpit (emails)
set -eu
URL="${KARDAN_URL:-http://localhost:8080}"
OPS="${MOCKOPS_URL:-http://localhost:8091}"
HERE="$(cd "$(dirname "$0")" && pwd)"

bold() { printf '\n\033[1m%s\033[0m\n' "$*"; }
step() {
  bold "== $1"
  shift
  for line in "$@"; do printf '   %s\n' "$line"; done
  if [ -n "${STEP_DELAY:-}" ]; then sleep "$STEP_DELAY"; else printf '   [Enter] '; read -r _; fi
}
post() { curl -sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' "$@"; echo; }

curl -fsS "$URL/healthz" >/dev/null || { echo "Kardan is not running at $URL (docker compose up --build)"; exit 1; }

step "07:55  Before the open" \
  "Everything is healthy. Set 'Acting as' in the top bar to a name, e.g. Priya." \
  "The counterparty service is up and settlement retries work."
post -d '{"counterpartyHealthy": true, "retries": "follow"}' "$OPS/api/state"

step "08:00  Market open: prices and pricing-engine warnings" \
  "Market data and a burst of pricing warnings arrive (Kafka + a tailed log file)." \
  "Pricing warnings fold into one digest per instrument per 5 minutes instead of dozens of messages."
"$HERE/scenario.sh" market-data
"$HERE/scenario.sh" pricing-burst

step "09:14  The counterparty starts timing out" \
  "counterparty-service now answers 503 and workflow retries fail."
post -d '{"counterpartyHealthy": false, "retries": "fail"}' "$OPS/api/state"

step "09:15  Settlements fail, trades are rejected" \
  "A settlement workflow times out (Appendix A), then trades and FIX orders for ACME-CAPITAL are rejected." \
  "Watch: one correlated incident for ACME-CAPITAL with dollars at risk and the nearest payment cut-off," \
  "the settlements on-call is phoned and texted (phone simulator), and the settlement-timeout runbook" \
  "starts on its own: health check, retry, then an escalation that needs a second person's approval."
"$HERE/scenario.sh" settlement-timeout
PACE=0.4 "$HERE/scenario.sh" rejection-burst
"$HERE/scenario.sh" counterparty-outage

step "09:17  Four eyes" \
  "Open the settlement alert: the escalation step waits for approval." \
  "The requester cannot approve their own step. Approve as someone else: reply 'APPROVE <code>' from" \
  "their phone in the simulator (:8090), use the link in the approval email (:8025), or switch 'Acting as'."

step "09:25  The counterparty recovers" \
  "counterparty-service is back; retries succeed. Resolve the remaining alerts from the queue," \
  "or let recovery events close them (the payments-api metric clears below)."
post -d '{"counterpartyHealthy": true, "retries": "follow"}' "$OPS/api/state"
"$HERE/scenario.sh" recovery

step "09:30  Evidence" \
  "Every acknowledgement, approval and action is in a hash-chained audit log." \
  "Each alert can export a Markdown incident report (alert sheet -> timeline, or /api/alerts/<id>/report)."
curl -sS "$URL/api/audit/verify"; echo

step "17:00  Learn from the day" \
  "Kardan looks at how alerts were actually handled and proposes rule changes (Proposals page)." \
  "Someone other than the proposer reviews the diff and applies it."
curl -sS -X POST "$URL/api/proposals/analyze" -d '{"days": 1, "minAlerts": 3}'; echo

step "Bonus  A schema nobody has seen" \
  "A collateral system Kardan has never heard of starts sending margin calls." \
  "Watch the 'New schema detected' notice: LEI, ISIN and amounts are recognised on arrival." \
  "Then open Teams -> Onboard a team, paste the same event, and have a live rule in about two minutes."
post -d '{
  "msgType": "MARGIN_CALL_ISSUED",
  "origin": "collateral-hub",
  "urgency": "HIGH",
  "callId": "MC-2026-11-0042",
  "principal": { "lei": "5493001KJTIIGC8Y1R12", "name": "ACME CAPITAL LLP" },
  "exposure": { "amount": 42500000, "ccy": "EUR", "isin": "DE0001102580" },
  "dueBy": "2026-11-21T16:00:00Z"
}' "$URL/ingest/collateral-hub"

bold "Done. Reset the business clock on the Teams page if you moved it."
