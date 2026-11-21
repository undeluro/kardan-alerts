# Load test results

Measured on 2026-11-21 with `cmd/loadgen` against the full compose stack. Every number here comes from
`loadtest/results.jsonl` (one JSON line per run, written by the tool); the console output of each series is in
`raw-1-worker.md` and `raw-3-workers.md`.

**Headline: 198,000 events, 144,900 alert SMS expected, 144,900 delivered, 0 lost, across 10 recorded runs.** With
three worker replicas, 500 new alerts per second reach the SMS provider in p99 42 ms, and the 10× market-open
burst (1,000 new alerts per second) in p99 0.7–4.9 s, with no message lost and the backlog cleared in seconds.

## Setup

| | |
|---|---|
| Machine | Apple M3 Pro laptop, Docker Desktop VM with 11 CPUs and 8 GB RAM |
| Everything on that one VM | Kardan API/ingest (1 replica), Kardan workers (1 or 3 replicas, `ROLES=processor,dispatcher`), Postgres 17 (`synchronous_commit=on`), Redpanda (single node, 1 core), Redis, the fake SMS gateway, Mailpit, Prometheus, Grafana, and the load generator itself |
| Events topic | 12 partitions |
| Per worker | `DISPATCH_WORKERS=16` concurrent sends, processor batches up to 2,000 events |

Numbers from one laptop running every component are a floor, not a ceiling: workers compete with Postgres,
Redpanda and the load generator for the same CPUs and disk.

## What is measured

- **End to end** (the headline latency): from the moment Kardan *accepted* the event at ingest to the moment the
  SMS provider *accepted* the message, measured by the fake gateway from the `eventReceivedAt` timestamp Kardan
  sends with every message. It includes the durable write to the log, processing (schema, facets, rules,
  dedup), the database transaction that writes the alert and its outbox row, and dispatch.
- **Server side**: the same interval from Kardan's own Prometheus histogram (`kardan_e2e_latency_seconds`),
  as a cross-check. The two agree within the histogram's bucket resolution.
- **Ingest ack**: how long an HTTP batch of 20 events takes to be acknowledged (only after Redpanda
  acknowledged the write).
- **Lost**: alert SMS that never reached the provider. The tool knows exactly which alerts it created (every
  event in "all new alerts" runs has a unique fingerprint; the realistic mix repeats 500 problems, and dedup must
  fold the repeats) and counts deliveries to a phone number unique to the run. A run fails if anything is
  missing after a 3–5 minute drain.

Scenarios: **steady** (constant rate, every event opens a new alert and an SMS, the worst case), **realistic**
(1,000 events/s where 90% repeat open problems and are folded by dedup), and **storm** (100 events/s, then 10×
for 20 s as at a market open, then back to 100). Each run starts with a 100-event warm-up and from the same
database state (the previous runs' synthetic alerts deleted and the tables vacuumed).

## Results

| Scenario | Workers | Events sent | Alert SMS expected / delivered / **lost** | Ingest ack p50 / p99 (ms) | End to end p50 / p95 / p99 (ms) | Server-side p50 / p95 / p99 (ms) |
|---|---|---|---|---|---|---|
| Steady 200 ev/s, all new alerts | 1 | 6,000 | 6,000 / 6,000 / **0** | 1.4 / 32 | 17 / 48 / 87 | 19 / 49 / 93 |
| Steady 200 ev/s, all new alerts | 3 | 6,000 | 6,000 / 6,000 / **0** | 1.8 / 22 | 23 / 66 / 117 | 23 / 82 / 146 |
| Steady 500 ev/s, all new alerts | 1 | 15,000 | 15,000 / 15,000 / **0** | 0.9 / 21 | 12 / 63 / 159 | 14 / 72 / 203 |
| Steady 500 ev/s, all new alerts | 3 | 15,000 | 15,000 / 15,000 / **0** | 0.8 / 10 | 11 / 27 / 42 | 15 / 34 / 49 |
| Realistic 1,000 ev/s, 10% new | 1 | 30,000 | 3,450 / 3,450 / **0** | 0.9 / 2,002 | 9 / 378 / 2,143 | 10 / 414 / 1,823 |
| Realistic 1,000 ev/s, 10% new | 3 | 30,000 | 3,450 / 3,450 / **0** | 1.0 / 22 | 8 / 90 / 126 | 8 / 39 / 84 |

Market-open storm, end-to-end latency per phase (ms), two runs per configuration:

| Storm run | Workers | Alert SMS delivered / **lost** | Ingest ack p99 (ms) | Before open, 100 ev/s: p50 / p99 | Market open, 1,000 ev/s: p50 / p95 / p99 | After open, 100 ev/s: p50 / p99 |
|---|---|---|---|---|---|---|
| 1 | 1 | 24,000 / **0** | 67 | 17 / 75 | 18 / 1,661 / 2,010 | 23 / 786 |
| 2 | 1 | 24,000 / **0** | 106 | 24 / 1,026 | 17 / 1,023 / 1,259 | 20 / 128 |
| 1 | 3 | 24,000 / **0** | 3,374 | 23 / 84 | 2,026 / 4,703 / 4,938 | 17 / 912 |
| 2 | 3 | 24,000 / **0** | 118 | 15 / 43 | 13 / 499 / 666 | 44 / 3,799 |

Independent check of the HTTP ingest edge with k6 (`make k6`: 50 then 250 batches of 20 events per second,
events that match no rule): 5,252 requests (105,040 events), 100% acknowledged with `202`, 0 failed, latency
median 0.9 ms, p95 8 ms, max 85 ms.

## Findings

1. **No loss under any load tested.** Every alert SMS was delivered in every run, including bursts that built a
   backlog of several seconds: accepted events wait in the durable log and the outbox until they are processed
   and sent.
2. **Deduplication works at load.** The realistic mix sends 30,000 events and Kardan opens exactly 3,450 alerts
   (one per distinct problem) and sends exactly 3,450 SMS.
3. **Scaling out helps where the work is.** Going from one worker to three cut p99 at 500 new alerts/s from
   159 ms to 42 ms, and for the realistic 1,000 events/s mix from 2.1 s to 126 ms. In the market-open burst
   three workers kept p99 at 0.7 s in the clean run (one worker: 1.3–2.0 s).
4. **At 200 events/s one worker is enough**; three are slightly slower at the tail (117 vs 87 ms p99) because of
   more coordination for little work.
5. **The burst is bounded by shared resources on this machine,** not by the worker count. During the burst
   the processing median stays at a few milliseconds and messages queue for dispatch, which writes to Postgres for
   every delivery. While measuring we combined the two updates per delivery into one statement (one commit
   instead of two, `internal/notify/dispatcher.go`); a single before/after check suggested a lower burst median,
   but that comparison was not part of the controlled series. On separate hosts (a dedicated Postgres, more
   Redpanda cores) the dispatch ceiling should rise with the number of workers; we have not measured that.

## Caveats

- **Host stalls.** Some runs show ingest acknowledgements of 2–3 s at p99 (the 1-worker realistic run, the first
  3-worker storm run). Kardan's ingest path does almost nothing besides waiting for Redpanda's acknowledgement,
  so these are stalls of the shared VM; latency in those runs is inflated accordingly. Both runs are kept in the
  tables rather than re-rolled.
- **Rebalancing pauses.** Right after scaling from one worker to three, the consumer group moves partitions and
  processing pauses for several seconds (an early run measured p50 5 s at 200 events/s straight after scaling).
  The series above start after a short settle run; production deployments should scale ahead of known peaks
  such as the market open.
- **One unexplained early result.** In a run made before per-run database resets were added, the tool reported
  2 of 3,410 SMS not delivered within its 3-minute drain while the host was stalling; its rows were deleted by
  the clean-up before they could be inspected, so it is unknown whether they arrived late or not at all. Two
  immediate repeats and every run reported here delivered everything. The tool now keeps a failing run's data
  for inspection.
- **How we got here.** The first measurements were wrong and are not reported: deliveries were counted
  globally (a previous run's backlog counted toward the next), "all clear" messages from one run's
  auto-resolved alerts landed in the next run, and Prometheus was queried before its last scrape. The tool now
  counts per-run phone numbers and alert messages only, cleans up each run before the next starts, and waits two
  scrape intervals before querying.

## Reproduce

```sh
docker compose up -d --build
make load-series                              # 1 worker
docker compose up -d --scale worker=3 && sleep 30
make load-series                              # 3 workers
```

Or a single run: `docker compose run --rm loadgen -scenario storm -rate 100 -duration 60s`. Watch it live on the
Grafana dashboard at http://localhost:3000 ("Kardan overview"). `make k6` runs an independent k6 check of the
HTTP ingest edge.
