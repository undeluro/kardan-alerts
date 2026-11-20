# Kardan Alerts

**Alerting that speaks money and deadlines.** Kardan gets the right alert to the right people on the channel they
prefer, without flooding them, for trading, settlement and payments operations. It accepts events of *any* shape
from any source, and teams decide what reaches them with rules they edit as data, not code.

> Hackathon 2026 — *Flexible Alerting & Notification Service*. Status: **Milestones 1–2 done** (skeleton +
> working end-to-end slice). See [Roadmap](#roadmap) for what is next.

## Quick start

Requirements: Docker with Compose v2. Nothing else.

```sh
docker compose up --build          # first build takes ~2 minutes
./samples/send.sh                  # Windows: .\samples\send.ps1
```

| What                     | Where                                                         |
|--------------------------|---------------------------------------------------------------|
| Live alert feed (UI)     | http://localhost:8080                                         |
| Every email sent         | http://localhost:8025 (Mailpit)                               |
| Prometheus metrics       | http://localhost:8080/metrics                                 |
| SMS / phone (simulated)  | `docker compose logs worker \| grep "simulated delivery"`      |

Scale the workers horizontally: `docker compose up -d --scale worker=3`.

## What works today

- **Any schema, zero code.** Events are kept verbatim. Rules address fields by path (`error.code`,
  `labels.severity`, `payload.notional`). Canonical *facets* (`$severity`, `$service`, `$type`, `$env`) are
  derived by heuristics, so one rule ("critical from payments-api") matches a Prometheus webhook, a workflow event
  and a business event that each spell severity differently. Non-JSON input (log lines, FIX, XML) is wrapped as
  `{"message": ...}` and is still matchable.
- **Rules as data** (`config/rules/*.yaml`): `all` / `any` / `not` trees, 15 operators (`eq`, `in`, `gt`, `glob`,
  `regex`, `severityAtLeast`, …), and routing and presentation per rule (title/body templates such as
  `Rejected {{payload.currency}} {{payload.notional | number}}`, chosen fields, severity, colour). All four
  example rules from the brief are included and pass tests.
- **No floods.** Repeats of the same problem fold into one alert with a counter: 500 identical errors become one
  alert ×500, two database writes and no re-paging. Resolving frees the fingerprint, so a recurrence opens a new
  alert.
- **No lost alerts.**
  - Ingest returns `202` only after the event log (Kafka API) acknowledges the event.
  - Alerts and their notifications are committed in one transaction (transactional outbox).
  - Delivery retries with exponential backoff and jitter, and dead-letters only after `MAX_ATTEMPTS`.
  - Tested: stop Mailpit, send an alert, start Mailpit → it is delivered on the next retry.
- **Live UI.** A server-sent-events feed per team (push, no refresh), ack/resolve, a detail drawer with delivery
  status per channel and the raw payload. Dark mode follows the OS.
- **Horizontal scaling by role.** One binary, `ROLES=api,ingest,processor,dispatcher` in any mix. The workers are
  stateless; state lives in Postgres (alerts, outbox), Redis (live fan-out) and Redpanda (durable event log).

## Architecture

```
 POST /ingest/{source} ──► ingest ──► [kardan.events, 12 partitions] ──► processor × N
 (Kafka / file / poll                                                    rules → fingerprint → batch fold
  sources: M3)                                                            │ one transaction:
                                                                          ▼ alerts upsert + outbox rows
                            browser ◄── SSE ◄── api × N ◄── Redis pub/sub ◄─┤
                                                                          ▼ NOTIFY
                                       email / sms / phone ◄── dispatcher × N (SKIP LOCKED, retries)
```

| Concern | How |
|---|---|
| Duplicate alerts across replicas | Partial unique index: at most one unresolved alert per fingerprint |
| Crash mid-batch | Offsets committed after the database commit; a replay folds into the same alert |
| Dispatcher crash | Rows are leased (`locked_until`); another replica picks them up |
| Channel outage | Rows wait in `retry` with backoff; other channels are unaffected |
| Live updates on any replica | Redis pub/sub fan-out; clients re-sync from REST on reconnect |

## Writing a rule

```yaml
# config/rules/rates-big-rejections.yaml
id: rates-big-rejections
team: rates
match:
  all:
    - { field: meta.schema,      op: glob, value: "trade.rejected.*" }
    - { field: payload.desk,     op: eq,   value: rates }
    - { field: payload.notional, op: gt,   value: 10000000 }
routing:
  - { to: team:rates, channels: [email, ui] }
presentation:
  title: "Rejected {{payload.currency}} {{payload.notional | number}} — {{payload.reason}}"
  fields: [Trade=payload.tradeId, Desk=payload.desk]
dedup: { key: "{{payload.tradeId}}" }
```

Try a rule against an event without saving it:

```sh
curl -s localhost:8080/api/rules/test -d '{"rule": {...}, "event": {...}}'
```

Template filters: `number`, `upper`, `lower`, `default "x"`, `truncate N`, `json`.

## API

| Method | Path | |
|---|---|---|
| POST | `/ingest/{source}` | one event (JSON, or text treated as a log line) |
| POST | `/ingest/{source}/batch` | JSON array or newline-delimited events |
| GET | `/api/alerts?team=&status=active\|resolved` | list |
| GET | `/api/alerts/{id}` | alert + deliveries |
| POST | `/api/alerts/{id}/ack` · `/resolve` | body `{"by": "name"}` |
| GET | `/api/stream?team=` | server-sent events |
| GET | `/api/rules` · POST `/api/rules/test` | loaded rules / dry run |
| GET | `/api/teams` · `/api/stats` · `/api/info` | directory, counters, facets and operators |

## Development

```sh
docker compose up -d postgres redis redpanda mailpit   # infrastructure only
go run ./cmd/kardan                                    # all roles, defaults point at the compose ports
cd web && npm install && npm run dev                   # UI with hot reload on :5173
go test ./...                                          # unit tests
```

Configuration is through environment variables with working defaults; see `internal/platform/config/config.go`.

## What is mocked

| Real system | Here |
|---|---|
| SMTP provider | Mailpit (real SMTP, captured) |
| SMS / phone gateway | Logged by the dispatcher (a fake gateway with a phone-simulator UI arrives in M4) |
| Kafka | Redpanda (Kafka API compatible, single node) |
| Authentication / SSO | None yet: actions record a free-text "acting as" name (entitlements: M7) |

## Roadmap

1. ~~Skeleton~~ ✔
2. ~~Vertical slice: HTTP ingest → rules → dedup → alerts → live feed → email~~ ✔
3. Kafka, file-tail, FIX and ISO 20022 sources; schema registry with finance field detection; rule CRUD with hot
   reload
4. Fake SMS/phone gateway; preferences with working hours and follow-the-sun on-call; masking per channel;
   circuit breakers
5. Grouping digests, throttling, auto-resolve, FX-normalised dollar impact, incident correlation
6. `aggregate` and `absence` rules, market calendars and cut-off countdowns
7. Runbooks (YAML / Markdown / free text) with actions, four-eyes approvals and a hash-chained audit trail
8. Rule builder with dry-run on recent events, onboarding wizard
9. AI-assisted runbook structuring and rule drafting (opt-in)
10. Load tests (p50/p99 measured, 1 vs 3 replicas)
11. Docs, demo script, video
