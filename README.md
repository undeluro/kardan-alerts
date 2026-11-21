# Kardan Alerts

**Alerting that speaks money and deadlines.** Kardan gets the right alert to the right people on the channel they
prefer, without flooding them, for trading, settlement and payments operations. It accepts events of *any* shape
from any source, and teams decide what reaches them with rules they edit as data, not code.

> Hackathon 2026 — *Flexible Alerting & Notification Service*. Status: **all 11 milestones done**. Start with the
> [five-minute demo](#five-minute-demo), the [measured performance](loadtest/results.md), the
> [architecture](docs/architecture.md), [what is mocked](docs/mocked.md) and the
> [AI usage, security and privacy statement](docs/ai-usage.md).

## Quick start

Requirements: Docker with Compose v2. Nothing else.

```sh
docker compose up --build          # first build takes ~2 minutes
./samples/send.sh                  # Windows: .\samples\send.ps1
```

Optional: `ANTHROPIC_API_KEY=... docker compose up --build` turns on Claude assistance for teams that opt in
(see [AI usage](docs/ai-usage.md)); everything works without it.

### Five-minute demo

```sh
./samples/rates-desk-day.sh        # "a day on the rates desk", one step per Enter
```

Market open, a counterparty outage that becomes one incident with dollars at risk and a cut-off countdown, a
runbook that runs itself and stops for four-eyes approval by SMS, recovery, the audit chain, Kardan proposing a
rule change from how the day went, and finally a schema nobody has seen onboarded in two minutes. The talk track
is in [docs/demo-script.md](docs/demo-script.md).

### Onboard a team in two minutes

Teams → **Onboard a team**: name the team, pick or add people, paste one event in any format (JSON, ISO 20022
XML, FIX, a log line), click the fields that matter, choose who gets it and how, review (the rule is tested on
the sample and backtested on the last 24 hours of real traffic), create, and send the sample as a live event.

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

Runbooks (Appendix B's settlement runbook runs by itself):

```sh
./samples/scenario.sh settlement-timeout     # health check passes, retry works, the alert resolves itself
curl -X POST localhost:8091/api/state -d '{"counterpartyHealthy":false}'  # or the switch on http://localhost:8091
./samples/scenario.sh settlement-timeout     # check fails, retry fails twice -> escalation waits for approval
# the on-call person gets "Reply APPROVE <code>": reply in the phone simulator (:8090), or approve on the alert page
```

| What                     | Where                                                         |
|--------------------------|---------------------------------------------------------------|
| Product overview         | http://localhost:8080/landing                                |
| Live alert feed (UI)     | http://localhost:8080                                         |
| Rules, schemas, sources  | http://localhost:8080/rules · /schemas · /sources             |
| Every email sent         | http://localhost:8025 (Mailpit)                               |
| SMS and calls            | http://localhost:8090 (phone simulator: reply, answer, inject provider failures) |
| Runbooks, audit trail    | http://localhost:8080/runbooks · /audit                       |
| What actions call        | http://localhost:8091 (mock operations: workflow engine, payments hub, trading controls…) |
| Deliveries, on-call      | http://localhost:8080/deliveries · /teams                     |
| Markets and cut-offs     | http://localhost:8080/markets                                 |
| Proposals, onboarding    | http://localhost:8080/proposals · /onboarding                 |
| Metrics and dashboards   | http://localhost:9090 (Prometheus) · http://localhost:3000 (Grafana, "Kardan overview") |

Scale the workers horizontally: `docker compose up -d --scale worker=3`.

### Load tests

```sh
make load         # 500 events/s for 30 s, every event a new alert and SMS; prints throughput, p50/p99, losses
make load-storm   # market open: 100 -> 1000 -> 100 events/s
make load-series  # the series behind loadtest/results.md
make k6           # k6 view of the HTTP ingest edge
```

`cmd/loadgen` creates its own fixtures through the API, measures end-to-end latency at the SMS gateway (event
accepted by Kardan -> provider accepted the SMS), cross-checks against the server-side Prometheus histogram, and
fails if a single notification is missing. Results: [loadtest/results.md](loadtest/results.md).

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
- **Runbooks in any shape** (`config/runbooks/` or pasted in the UI).
  - The five shapes from the brief all parse into the same steps: structured YAML (Appendix B as given), a
    Markdown page, a checklist, a wiki link and free text from a chat.
  - Free text and Markdown are read for instructions: "Restart the pricing-engine instance named in the alert, wait
    2 minutes, and if latency is still high, call the pricing on-call person" becomes restart (of the alert's own
    instance), wait 2 min, and a conditional call to the pricing on-call. Sentences with no instruction stay as
    context, guessed actions are marked "read from text", and "needs approval from the team lead" means approval.
  - A runbook attaches to alerts by rule, by schema, or by a `matches:` condition, so a judge's new runbook needs
    no code.
  - Steps show on the alert with values filled in (workflow id, service, instrument…). Email gets the full steps,
    SMS a short link, and a call the first step, all masked like the rest of the message.
  - The editor previews how Kardan reads the text, flags unknown actions and missing values, and converts prose
    into structured YAML to correct.
- **Steps that do things.**
  - Built-in actions are `http_get`, `call_webhook`, `wait` and `escalate`. Every other kind is a few lines of
    `config/actions.yaml` (an HTTP call with a templated URL): retry a workflow, restart a service, open a ticket,
    post to a channel, notify Risk, fail over a feed or region, roll back, resubmit a payment, pause a desk.
  - Requests go only to allow-listed hosts (runbook text is user input), with an idempotency key per attempt.
  - Auto runbooks run on their own: Appendix B's settlement runbook checks counterparty health, retries the
    workflow (twice, because the next step says "if retry fails twice"), and either resolves the alert or asks for
    approval to escalate.
  - Steps are a durable queue executed by any worker (SKIP LOCKED with a lease); `then: resolve` closes the alert
    when the fix works.
- **Four-eyes approvals.**
  - High-risk actions (resubmit a payment, roll back, fail over a region, pause trading) always need a second
    person, whatever the runbook says. Others need approval when the runbook asks.
  - The approver is never the requester, and high-risk steps need a member of the alert's team. This is enforced on
    the server for every path: UI, SMS reply `APPROVE 2C0D`, or a signed one-time link in the approval email.
  - Approvers are the alert's on-call and team, minus the requester.
- **Tamper-evident audit trail.**
  - Acknowledgements, approvals and refusals, runbook actions with their results, rule, runbook and schema changes
    and clock shifts are recorded append-only. Each entry carries the hash of the one before.
  - `GET /api/audit/verify` (and the Audit page) recomputes the chain: editing any entry, or deleting one, is
    reported at that exact entry.
  - Each alert has a "who did what" timeline and a Markdown incident report export (summary, timeline, steps with
    requester and approver, audit hashes and chain status; sensitive values masked).
- **Live UI.** A server-sent-events feed per team (push, no refresh), ack/resolve, and a detail drawer with each
  delivery's channel, recipient, reason, fallbacks and the raw payload. The Deliveries page shows channel health
  and breaker state, the Teams page shows on-call now. In demo mode a shared business clock lets you jump to
  21:00 or next week to show schedules. Dark by default, with a light theme one click away.
- **Rule builder and backtests.** A visual AND/OR/NOT builder and the YAML stay in sync (comments kept). Every
  draft is tested on a sample event and **backtested on the last 1–24 hours of recorded events** (replayed from
  the durable log, nothing stored or sent): events matched, alerts it would open, threshold crossings for window
  rules, and a timeline.
- **People, teams and preferences in the UI.** An ordered preferences matrix (severity × channel, working hours,
  days) with a live "who gets what right now" preview, on-call rotations, and an onboarding wizard. UI edits are
  stored over `teams.yaml`, validated before saving, labelled, and revertible.
- **Incident view.** Money at risk, nearest cut-off, teams and every alert of a correlated incident.
- <a id="proposals-and-fatigue-analysis"></a>**Proposals and fatigue analysis.** Anyone can propose a change to
  a rule or runbook from an alert or the editor; a different, named person reviews the diff and applies it.
  Kardan proposes changes itself from how alerts were handled: rules whose alerts clear on their own, that people
  close within minutes while being paged, or that nobody acknowledges.
- **Optional Claude assistance.** Draft a rule from a sentence; structure a pasted runbook into steps with
  catalogue actions and approvals. Opt-in per team, only text and field names leave the service, output is
  schema-constrained and validated, risky actions always need approval, and built-in readers answer when Claude is
  off. Details: [docs/ai-usage.md](docs/ai-usage.md).
- **Measured.** Prometheus and a Grafana dashboard in the stack, a load generator that checks for lost
  notifications, and published numbers for one and three workers: [loadtest/results.md](loadtest/results.md).
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

## Writing a runbook

Any of these work; front matter (or the editor's fields) says which alerts it belongs to.

```yaml
# config/runbooks/settlement-timeout.yaml (Appendix B, plus auto and then: resolve)
runbook: settlement-timeout
auto: true                       # run by itself when an alert opens
matches: { type: WORKFLOW_FAILED, "error.code": TIMEOUT }
steps:
  - check: "Is counterparty-service healthy?"
    action: http_get
    url: "http://counterparty-service/health"
  - action: retry_workflow
    workflowId: "{{workflowId}}"
    approval: none
    then: resolve                # close the alert when this works
  - if: "retry fails twice"
    action: escalate
    team: counterparty-support
    approval: required
```

```markdown
---
runbook: pricing-latency
matches: { $service: "pricing-engine-*" }
---
When pricing latency goes over a second, it's usually the cache. Restart the pricing-engine instance named in
the alert, wait 2 minutes, and if latency is still high, call the pricing on-call person.
```

A new kind of action is an entry in `config/actions.yaml`:

```yaml
- type: resubmit_payment
  label: "Resubmit payment {{endToEndId}}"
  risk: high                     # always four-eyes
  params: [endToEndId]
  request: { method: POST, url: "{{ops}}/payments/{{endToEndId}}/resubmit" }
```

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
| GET · POST | `/api/runbooks` | list / create (`{source, name, team, matches, rules, auto}` or raw text) |
| GET · PUT · DELETE | `/api/runbooks/{id}` | detail with structured YAML / update / delete (or remove a UI override) |
| POST | `/api/runbooks/parse` | preview: format, steps, actions, approvals, problems, YAML |
| GET | `/api/actions` | action kinds with risk and parameters |
| GET | `/api/alerts/{id}/runbooks` | attached runbooks with filled steps and their runs |
| POST | `/api/alerts/{id}/runbooks/{rb}/steps/{n}` | run a step (or mark a manual one done) as `{"by"}` |
| POST | `/api/runs/{id}/approve` · `/reject` | four-eyes decision as `{"by"}` |
| GET | `/approve/{token}` | signed one-click approval page (from approval emails) |
| GET | `/api/audit?prefix=&subject=` · `/api/audit/verify` | audit trail · chain verification |
| GET | `/api/alerts/{id}/timeline` · `/report` | who did what · Markdown incident report |
| POST | `/inbound/sms` · `/inbound/call` | provider webhooks for replies (ACK, RESOLVE, APPROVE, REJECT) and keypad presses (token required) |
| GET | `/api/teams` · `/api/stats` · `/api/info` | directory (with origin of each entry), counters, facets, operators, config errors |
| PUT · DELETE | `/api/teams/{id}` · `/api/people/{id}` | save a team or person over `teams.yaml` (`If-Match: <version>`) / revert to the file |
| POST | `/api/rules/backtest` | replay recent events through a draft: `{ruleYaml, hours}` |
| POST | `/api/events/inspect` | parse an event, list fields with finance detection and facets (nothing stored) |
| GET | `/api/ai/status` | whether Claude is configured and which teams opted in |
| POST | `/api/ai/rule-draft` · `/api/ai/runbook` | rule draft from `{sentence, team}` · steps from `{text, team}` |
| GET · POST | `/api/proposals` · `/api/proposals/{id}` | review queue / propose `{kind, target, title, rationale, body}` |
| POST | `/api/proposals/{id}/accept` · `/reject` | decide (a named person other than the proposer); accepting applies the change |
| POST | `/api/proposals/analyze` | fatigue analysis over `{days}`: files suggestions for noisy rules |

## Development

```sh
docker compose up -d postgres redis redpanda mailpit   # infrastructure only
go run ./cmd/kardan                                    # all roles, defaults point at the compose ports
cd web && npm install && npm run dev                   # UI with hot reload on :5173
go test ./...                                          # unit tests
```

Configuration is through environment variables with working defaults; see `internal/platform/config/config.go`.

## What is mocked

Summary below; the full list with what it takes to make each part real is in [docs/mocked.md](docs/mocked.md).

| Real system | Here |
|---|---|
| SMTP provider | Mailpit (real SMTP, captured) |
| SMS / voice provider | `cmd/fakegw`: same contract as a real provider (HTTP API, idempotency keys, inbound reply webhooks), with a phone simulator and failure injection. A real provider is one more `notify.Channel` |
| Kafka | Redpanda (Kafka API compatible, single node) |
| Market calendars | `config/calendars.yaml` with illustrative hours, cut-offs and 2026 holidays; a real deployment would load them from a reference-data service |
| Operations systems runbooks act on | `cmd/mockops`: workflow engine, service manager, ticketing, chat, risk, market data, regions, deployments, payments hub and trading controls, with idempotency and a page to break `counterparty-service`. Real systems are entries in `config/actions.yaml` |
| Authentication / SSO | The "acting as" box picks a person from `teams.yaml`; approvals check that person (four-eyes, team entitlement). SMS replies are identified by phone number, email links by signature. A real deployment would take identity from SSO |

## Roadmap

1. ~~Skeleton~~ ✔
2. ~~Vertical slice: HTTP ingest → rules → dedup → alerts → live feed → email~~ ✔
3. ~~Kafka, file-tail, FIX and ISO 20022 sources; schema registry with finance field detection; rule CRUD with hot
   reload~~ ✔
4. ~~Fake SMS/phone gateway; preferences with working hours and follow-the-sun on-call; masking per channel;
   circuit breakers~~ ✔
5. ~~Grouping digests, throttling, auto-resolve, FX-normalised dollar impact, incident correlation~~ ✔
6. ~~`aggregate` and `absence` rules, market calendars, cut-off countdowns and escalation ladders~~ ✔
7. ~~Runbooks (YAML / Markdown / checklist / link / free text) with actions, four-eyes approvals and a hash-chained
   audit trail~~ ✔
8. ~~Rule builder with backtests on recorded events, editable preferences, incident view, onboarding wizard~~ ✔
9. ~~Claude-assisted rule drafting and runbook structuring (opt-in), proposal queue, fatigue suggestions~~ ✔
10. ~~Prometheus/Grafana, load generator, market-open storm, 1 vs 3 workers ([results](loadtest/results.md))~~ ✔
11. ~~Docs ([architecture](docs/architecture.md), [mocked](docs/mocked.md), [AI usage](docs/ai-usage.md)), demo
    script and video talk track~~ ✔

Next, beyond the hackathon scope: regulatory incident clocks (DORA-style report deadlines with pre-filled drafts),
impact tolerances for important business services, and SSO-backed identity.
