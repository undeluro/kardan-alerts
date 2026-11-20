# Kardan Alerts

**Alerting that speaks money and deadlines.** Kardan gets the right alert to the right people on the channel they
prefer, without flooding them, for trading, settlement and payments operations. It accepts events of *any* shape
from any source, and teams decide what reaches them with rules they edit as data, not code.

> Hackathon 2026 — *Flexible Alerting & Notification Service*. Status: **Milestones 1–4 done** (end-to-end
> slice; Kafka, log-file, FIX and ISO 20022 sources; schema registry with finance field detection; live rule
> editing; reliable delivery with on-call, preferences, masking, circuit breakers and fallbacks). See [Roadmap](#roadmap) for what is next.

## Quick start

Requirements: Docker with Compose v2. Nothing else.

```sh
docker compose up --build          # first build takes ~2 minutes
./samples/send.sh                  # Windows: .\samples\send.ps1
```

`send` pushes samples through every source type: HTTP webhooks (Alertmanager JSON, ISO 20022 XML), Kafka topics
and tailed log files (regex-parsed pricing logs, a FIX engine log).

| What                     | Where                                                         |
|--------------------------|---------------------------------------------------------------|
| Live alert feed (UI)     | http://localhost:8080                                         |
| Rules, schemas, sources  | http://localhost:8080/rules · /schemas · /sources             |
| Every email sent         | http://localhost:8025 (Mailpit)                               |
| SMS and calls            | http://localhost:8090 (phone simulator: reply, answer, inject provider failures) |
| Deliveries, on-call      | http://localhost:8080/deliveries · /teams                     |
| Prometheus metrics       | http://localhost:8080/metrics                                 |

Scale the workers horizontally: `docker compose up -d --scale worker=3`.

## What works today

- **Many sources, configuration only** (`config/sources.yaml`, applied live):
  - **HTTP webhooks:** `POST /ingest/<any-id>`, no declaration needed.
  - **Kafka topics:** a consumer group per source, so ingest replicas share partitions; offsets are committed only
    after the event is durably appended.
  - **Log files:** tailed with globs and rotation. A Redis lease gives each file source to exactly one replica,
    with fail-over.
- **Many formats, auto-detected.**
  - JSON.
  - **FIX** tag=value: named tags and decoded enums, e.g. `OrdStatusName: Rejected`, plus a derived `notional`.
  - **ISO 20022** XML: the summary is lifted to the top level (`status: RJCT`, `reasonCode: AC04` →
    `ClosedAccountNumber`, amount, currency, IBANs).
  - Generic XML, logfmt, regex with named groups, and plain text with level detection.
  - Nothing is ever dropped: the original text is kept next to the parsed payload.
- **Any schema, zero code.**
  - Rules address fields by path (`error.code`, `payload.notional`).
  - Canonical *facets* (`$severity`, `$service`, `$type`, `$env`, `$instrument`, `$counterparty`, `$currency`,
    `$notional`, `$tradeId`, `$desk`, `$account`) let one rule work across schemas that spell things differently.
- **Schema registry with finance detection.**
  - Every new payload structure is catalogued and announced live in the UI ("New schema detected").
  - Fields are classified by real check digits and names: ISIN (Luhn), LEI and IBAN (mod 97), currencies, amounts,
    references, counterparties, settlement dates.
  - Sensitive fields (IBANs, party names) are flagged for masking.
  - Facets are mapped automatically and can be overridden per schema in the UI. Example: map an unknown
    `criticity: HIGH` field to `$severity`, and it becomes `error`.
- **Rules as data, edited live.**
  - Rules are YAML files in `config/rules/` or rules created and edited in the UI/API. A UI edit of a file rule is
    stored as an override, and "revert" restores the file.
  - The language: `all` / `any` / `not` trees, 15 operators, templated titles and bodies, chosen fields, severity
    and colour.
  - Every save is validated, including unknown teams and recipients, and versioned with optimistic locking.
  - Changes reach every replica within seconds through Redis pub/sub, a config-directory poll (works on Docker
    Desktop bind mounts) and a periodic version check.
  - The editor dry-runs the rule against a sample event (JSON, XML, FIX or a log line) through the same parser and
    facet mapping as production.
  - On a schema page, tick fields and get a drafted rule.
- **No floods.** Repeats of the same problem fold into one alert with a counter: 500 identical errors become one
  alert ×500, two database writes and no re-paging. Resolving frees the fingerprint, so a recurrence opens a new
  alert.
- **No lost alerts.**
  - Ingest returns `202` only after the event log (Kafka API) acknowledges the event.
  - Alerts and their notifications are committed in one transaction (transactional outbox).
  - Delivery retries with exponential backoff and jitter, and dead-letters only after `MAX_ATTEMPTS`.
  - Tested: stop Mailpit, send an alert, start Mailpit → it is delivered on the next retry.
- **Right person, right channel, right time.**
  - Routes target a team, **whoever is on call** (weekly or daily rotation, follow-the-sun shifts across time
    zones, holiday overrides), or a person.
  - Channels can be fixed or `preferences`: each person's severity → channel rules with working hours in their
    own time zone. Anna's "SMS 08:00–20:00, otherwise email" from the brief is literally her preference.
  - Every delivery records why it went to that person on that channel.
- **Replies close the loop.**
  - Replying `ACK K7Q2` or `RESOLVE K7Q2` to an SMS, or pressing 1 on an alert call, acknowledges or resolves the
    alert as that person.
  - Inbound webhooks are authenticated, so a forged message cannot silence an alert.
- **Masking per channel.**
  - Fields are classified as public, internal or confidential: by detected finance kind (IBAN, party names,
    contact details…), by explicit paths in `config/classification.yaml`, and by value (anything IBAN-shaped).
  - SMS and calls get public data only, email internal, the UI everything (sensitive fields show a 🔒).
  - A payment rejection email reads `Debtor: A. I. G. · IBAN ****3000`.
- **Delivery that survives provider outages.**
  - Each channel has a circuit breaker: after 5 consecutive failures it opens, and deliveries wait without
    burning their retry budget.
  - A parked fallback (phone → SMS → email, pre-rendered with the right masking) is activated after 3 failed
    attempts or 30 s undelivered, and cancelled if the primary gets through.
  - Permanent errors (bad number) go straight to dead letters, which can be re-sent from the Deliveries page.
  - Tested: provider down → fallback email in 3 s → breakers open → provider back → probe, close, backlog
    delivered, nothing lost.
- **Live UI.** A server-sent-events feed per team (push, no refresh), ack/resolve, and a detail drawer with each
  delivery's channel, recipient, reason, fallbacks and the raw payload. The Deliveries page shows channel health
  and breaker state, the Teams page shows on-call now. In demo mode a shared business clock lets you jump to
  21:00 or next week to show schedules. Dark mode follows the OS.
- **Horizontal scaling by role.** One binary, `ROLES=api,ingest,processor,dispatcher` in any mix. The workers are
  stateless; state lives in Postgres (alerts, outbox), Redis (live fan-out) and Redpanda (durable event log).

## Architecture

```
 HTTP webhook ─┐  parse (auto: json / fix / iso20022
 Kafka topics ─┼► / xml / logfmt / regex / text) ──► [kardan.events, 12 partitions] ──► processor × N
 Log files ────┘  (file sources leased to one replica)                   schema registry → facets → rules
                                                                          → fingerprint → batch fold
                                                                          │ one transaction:
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
| Config changes on every replica | Redis notice + config-dir poll + DB version check; atomic rule-set swap |
| Tailing a file from many replicas | Redis lease per file source; others stand by and take over |
| SMS / voice provider outage | Per-channel circuit breaker, deferred retries, standby fallback channel, dead letters + re-send |
| Duplicate sends to the provider | Notification id sent as `Idempotency-Key`; claims sized to free workers so leases never lapse |

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

Create or change rules without touching files (YAML or JSON bodies), or dry-run one first:

```sh
curl -s localhost:8080/api/rules -H 'Content-Type: application/yaml' --data-binary @my-rule.yaml
curl -s localhost:8080/api/rules/test -d '{"ruleYaml": "...", "raw": "<any event text: JSON, XML, FIX, log line>"}'
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
| GET · POST | `/api/rules` | list / create (YAML or JSON) |
| GET · PUT · DELETE | `/api/rules/{id}` | detail with YAML / update (`If-Match: <version>`) / delete or revert |
| POST | `/api/rules/test` | dry run: `{ruleYaml or rule, event or raw}` |
| GET | `/api/schemas` · `/api/schemas/{id}` | catalogue, fields, finance detection, facet values |
| PUT | `/api/schemas/{id}` | rename, set facet mappings `{"facets": {"$severity": "criticity"}}` |
| GET | `/api/sources` | configured sources with live status |
| GET | `/api/deliveries?status=queued\|dead\|sent&channel=` | outbox with reasons and fallbacks |
| POST | `/api/notifications/{id}/resend` | re-queue a dead or waiting delivery |
| GET | `/api/channels` | per-channel health, latency and circuit-breaker state |
| GET | `/api/oncall` · GET/PUT `/api/clock` | who is on call now · business clock (demo mode) |
| POST | `/inbound/sms` · `/inbound/call` | provider webhooks for replies and keypad presses (token required) |
| GET | `/api/teams` · `/api/stats` · `/api/info` | directory, counters, facets, operators, config errors |

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
| SMS / voice provider | `cmd/fakegw`: same contract as a real provider (HTTP API, idempotency keys, inbound reply webhooks), with a phone simulator and failure injection. A real provider is one more `notify.Channel` |
| Kafka | Redpanda (Kafka API compatible, single node) |
| Authentication / SSO | None yet: actions record a free-text "acting as" name (entitlements: M7) |

## Roadmap

1. ~~Skeleton~~ ✔
2. ~~Vertical slice: HTTP ingest → rules → dedup → alerts → live feed → email~~ ✔
3. ~~Kafka, file-tail, FIX and ISO 20022 sources; schema registry with finance field detection; rule CRUD with hot
   reload~~ ✔
4. ~~Fake SMS/phone gateway; preferences with working hours and follow-the-sun on-call; masking per channel;
   circuit breakers~~ ✔
5. Grouping digests, throttling, auto-resolve, FX-normalised dollar impact, incident correlation
6. `aggregate` and `absence` rules, market calendars and cut-off countdowns
7. Runbooks (YAML / Markdown / free text) with actions, four-eyes approvals and a hash-chained audit trail
8. Rule builder with dry-run on recent events, onboarding wizard
9. AI-assisted runbook structuring and rule drafting (opt-in)
10. Load tests (p50/p99 measured, 1 vs 3 replicas)
11. Docs, demo script, video
