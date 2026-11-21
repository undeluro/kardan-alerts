#!/bin/sh
# Runs the measured load-test series against the running stack.
# Usage: loadtest/run-series.sh <label-suffix>   (e.g. "1 worker", "3 workers")
#
# Before every run, the previous runs' synthetic alerts are deleted and the
# tables vacuumed, so each run starts from the same database state (otherwise
# later runs execute on a bigger table with autovacuum in the middle).
set -e
cd "$(dirname "$0")/.."
suffix="${1:-1 worker}"
reset() {
  docker compose exec -T postgres psql -q -U kardan -d kardan \
    -c "DELETE FROM alerts WHERE rule_id = 'loadtest-sms'" \
    -c "VACUUM ANALYZE alerts" -c "VACUUM ANALYZE notifications" >/dev/null
}
lg() { reset; docker compose run --rm loadgen -drain 5m -out /loadtest/results.jsonl "$@"; }
lg -scenario steady    -rate 200  -duration 30s -label "Steady 200 ev/s, every event a new alert ($suffix)"
lg -scenario steady    -rate 500  -duration 30s -label "Steady 500 ev/s, every event a new alert ($suffix)"
lg -scenario storm     -rate 100  -duration 60s -label "Market-open storm 100 -> 1000 -> 100 ev/s ($suffix), run 1"
lg -scenario storm     -rate 100  -duration 60s -label "Market-open storm 100 -> 1000 -> 100 ev/s ($suffix), run 2"
lg -scenario realistic -rate 1000 -duration 30s -label "Realistic mix 1000 ev/s, 10% new alerts ($suffix)"
