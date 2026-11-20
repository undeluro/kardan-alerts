# Kardan Alerts

**Alerting that speaks money and deadlines.** Kardan gets the right alert to the right people on the channel they
prefer, without flooding them, for trading, settlement and payments operations. It accepts events of *any* shape
from any source, and teams decide what reaches them with rules they edit as data, not code.

> Hackathon 2026 — *Flexible Alerting & Notification Service*. Status: **Milestones 1–6 done** (end-to-end
> slice; Kafka, log-file, FIX and ISO 20022 sources; schema registry with finance field detection; live rule
> editing; reliable delivery with on-call, preferences, masking, circuit breakers and fallbacks; money-at-risk
> ranking, incident correlation, grouping windows, throttling and auto-resolve; window totals, missing-event
> detection, market calendars, cut-off countdowns and escalation ladders). See [Roadmap](#roadmap) for what is next.

## Quick start

Requirements: Docker with Compose v2. Nothing else.

```sh
docker compose up --build          # first build takes ~2 minutes
./samples/send.sh                  # Windows: .\samples\send.ps1
```

`send` pushes samples through every source type: HTTP webhooks (Alertmanager JSON, ISO 20022 XML), Kafka topics
and tailed log files (regex-parsed pricing logs, a FIX engine log). Then play the scenarios:

```sh
./samples/scenario.sh counterparty-outage   # settlement, trade and FIX events for one counterparty -> one incident
./samples/scenario.sh pricing-burst         # many pricing warnings -> one alert per instrument per 5-minute window
./samples/scenario.sh recovery              # Alertmanager "resolved" -> alert closes, "all clear" to whoever was paged
PACE=1 ./samples/scenario.sh rejection-burst  # one per second: USD 50M rejected fires on the 3rd, "5 rejections" on the 5th
```

Time-aware rules (business clock in demo mode, so you can jump past a deadline):

```sh
./samples/scenario.sh eod-positions          # 2 of 3 custodians deliver their end-of-day positions file
curl -X PUT localhost:8080/api/clock -d '{"at":"2026-11-20T23:31:00Z"}'   # 18:31 New York: JPM-CUSTODY missing
./samples/scenario.sh eod-late-file          # the late file arrives -> alert resolves, "all clear" sent
curl -X PUT localhost:8080/api/clock -d '{"at":"2026-11-26T23:31:00Z"}'   # Thanksgiving: NYSE holiday, nothing fires
./samples/scenario.sh market-data            # price ticks; stop sending during London hours -> "no tick for 30 s"
curl -X PUT localhost:8080/api/clock -d '{"offsetMinutes":0}'             # back to real time
```

| What                     | Where                                                         |
|--------------------------|---------------------------------------------------------------|
| Live alert feed (UI)     | http://localhost:8080                                         |
| Rules, schemas, sources  | http://localhost:8080/rules · /schemas · /sources             |
| Every email sent         | http://localhost:8025 (Mailpit)                               |
| SMS and calls            | http://localhost:8090 (phone simulator: reply, answer, inject provider failures) |
| Deliveries, on-call      | http://localhost:8080/deliveries · /teams                     |
| Markets and cut-offs     | http://localhost:8080/markets                                 |
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
- **Ranked by money at risk.**
  - Amounts are normalised to USD (`$notionalUsd` facet, rates in `config/fx.yaml`, hot-reloaded).
  - Every alert gets an impact score (0–100): severity plus money on a log scale. The feed sorts by impact,
    money at risk or recency.
- **Incidents, not alert storms.**
  - Alerts that share a counterparty, an instrument or a service within 15 minutes fold into one incident, across
    teams and sources (`config/correlation.yaml`).
  - The counterparty scenario becomes one incident: "ACME-CAPITAL — 4 alerts, settlements + rates + e-trading,
    $413.6M at risk".
- **Less noise.**
  - `group: {by: [instrument], window: 5m}` gives "one alert per instrument every 5 minutes", with one digest per
    window.
  - Per-person throttling (e.g. 5 SMS per 10 min, critical exempt) holds the excess and sends one summary.
  - `autoResolve` closes alerts when the source reports recovery (Alertmanager `status: resolved`) or after a
    quiet period, and sends "all clear" to whoever was paged.
  - All of this is driven by a sweeper that is safe to run on every replica.
- **Totals over time, not just single events** (`kind: aggregate`).
  - A sliding window per key, counted or summed: "5 rejections for one counterparty in 10 minutes", "more than
    USD 50M of rejected notional per counterparty in an hour" (amounts converted to USD before summing).
  - Windows live in Redis sorted sets keyed by event id, so a replayed batch never double counts.
  - The alert opens when the total crosses the threshold, keeps the running total in its title and resolves
    itself, with an all-clear, when the total falls back below.
- **Silence is an alert too** (`kind: absence`).
  - Deadline: "each custodian's EOD positions file by 18:30 New York on every NYSE business day". Weekends and
    holidays expect nothing; the alert names the missing sender and resolves when the late file arrives.
  - Heartbeat: "a tick for every instrument at least every 30 s while London trades". Outside market hours
    silence is expected.
- **Market calendars and cut-offs** (`config/calendars.yaml`, hot-reloaded; illustrative T2, Fedwire, CHAPS, CLS,
  London and NYSE schedules with 2026 holidays).
  - `during: {calendar: XLON, otherwise: info}`: quote latency is critical while London trades, and only
    information (UI only, nobody paged) at night.
  - `cutoff: auto` ties an alert to the next cut-off for its currency. The alert shows "⏱ 44 min to Fedwire customer
    transfers", and up to 20 impact points are added over the last two hours, so the feed reorders itself as
    deadlines approach.
  - `escalation:` ladders notify the next level when nobody acknowledges in time. Within an hour of the cut-off the
    delays shrink with the time left, down to 20%: a 10-minute step fires after 2 minutes when the cut-off is
    minutes away.
  - Rules that name an unknown calendar or cut-off are rejected when saved.
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
# optional:
# group:       { by: [instrument], window: 5m }      # one alert per instrument per window, digest at close
# autoResolve: { when: { field: status, op: eq, value: resolved }, after: 2h }
# during:      { calendar: XLON, otherwise: info }   # outside London hours: downgrade, UI only (or "suppress")
# cutoff:      auto                                  # race the currency's payment cut-off (or a cut-off name)
# escalation:  [{ after: 15m, to: oncall:rates, channels: [phone] }]
```

Time-aware rule kinds (`kind: event` is the default):

```yaml
kind: aggregate                    # fires when a window total crosses a threshold
aggregate: { by: [$counterparty], window: 1h, sum: $notionalUsd, threshold: 50000000 }   # omit sum to count
presentation:
  title: "{{$key}}: USD {{$sum | number}} rejected in {{$window}} ({{$count}} trades)"
---
kind: absence                      # fires when the expected event does not arrive
absence: { by: custodian, expect: [BNYM, STATESTREET], deadline: "18:30", calendar: XNYS }
# or a heartbeat:  absence: { by: instrument, every: 30s, calendar: XLON }
presentation:
  title: "EOD positions from {{$key}} not received by {{$deadline}}"   # also $lastSeen, $silentFor, $reason
```

Create or change rules without touching files (YAML or JSON bodies), or dry-run one first:

```sh
curl -s localhost:8080/api/rules -H 'Content-Type: application/yaml' --data-binary @my-rule.yaml
curl -s localhost:8080/api/rules/test -d '{"ruleYaml": "...", "raw": "<any event text: JSON, XML, FIX, log line>"}'
```

Template filters: `number`, `upper`, `lower`, `default "x"`, `truncate N`, `json`. They also work in presentation
fields: `Rejected USD=$sum | number`.

## API

| Method | Path | |
|---|---|---|
| POST | `/ingest/{source}` | one event (JSON, or text treated as a log line) |
| POST | `/ingest/{source}/batch` | JSON array or newline-delimited events |
| GET | `/api/alerts?team=&status=active\|resolved&sort=impact\|money\|recent&incident=` | list |
| GET | `/api/incidents?status=open` · `/api/incidents/{id}` | correlated incidents with totals · incident + alerts |
| GET | `/api/fx` | FX rates used for money at risk |
| GET | `/api/calendars` | calendars at business time: open/closed, holidays, next cut-offs |
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
| Market calendars | `config/calendars.yaml` with illustrative hours, cut-offs and 2026 holidays; a real deployment would load them from a reference-data service |
| Authentication / SSO | None yet: actions record a free-text "acting as" name (entitlements: M7) |

## Roadmap

1. ~~Skeleton~~ ✔
2. ~~Vertical slice: HTTP ingest → rules → dedup → alerts → live feed → email~~ ✔
3. ~~Kafka, file-tail, FIX and ISO 20022 sources; schema registry with finance field detection; rule CRUD with hot
   reload~~ ✔
4. ~~Fake SMS/phone gateway; preferences with working hours and follow-the-sun on-call; masking per channel;
   circuit breakers~~ ✔
5. ~~Grouping digests, throttling, auto-resolve, FX-normalised dollar impact, incident correlation~~ ✔
6. ~~`aggregate` and `absence` rules, market calendars, cut-off countdowns and escalation ladders~~ ✔
7. Runbooks (YAML / Markdown / free text) with actions, four-eyes approvals and a hash-chained audit trail
8. Rule builder with dry-run on recent events, onboarding wizard
9. AI-assisted runbook structuring and rule drafting (opt-in)
10. Load tests (p50/p99 measured, 1 vs 3 replicas)
11. Docs, demo script, video
