# Kardan Alerts — implementation plan (Go + React)

## Context

- **Source:** Hackathon 2026 brief "Flexible Alerting & Notification Service" (photos `D:\kardan-alerts\task\*.HEIC`).
  Repo `D:\kardan-alerts` is empty (README + initial commit, origin `github.com/undeluro/kardan-alerts`).
- **Build:** a service that ingests events from ≥2 source types in *any* schema, lets teams define
  runtime-editable rules (filter on any field, AND/OR, per-rule routing + presentation), delivers on the
  preferred channel (email / SMS / phone / UI — simulated is fine) **without losing alerts**, has a live UI
  (ack/resolve, manage rules & preferences, propose fixes), dedups/groups, and attaches runbooks (YAML,
  Markdown, checklist, link, free text) whose steps are filled with event values and can be actions behind approval.
- **Judged on:** flexibility, horizontal scalability & state handling, UX (fresh clone → demo, README, time to
  onboard a team), **measured** throughput + p50/p99 ingest→delivery latency (load-test script required),
  implementation level + what is mocked, innovation, ≤5-min video, AI-usage + security/privacy statement.
- **Demo trap:** judges bring an **unseen schema** and an **unseen runbook** → both must work with zero code.
- **Audience:** Goldman Sachs hackathon → the product must be visibly useful for **capital-markets and payments
  operations** (trading desks, settlements, payments, market data, risk), not just a generic alert router.

## Decisions (agreed)

| Topic | Choice |
|---|---|
| Architecture | **P1 — one binary, scaled by roles** (`ROLES=ingest,processor,dispatcher,api`), stateless replicas |
| Stack | **Go 1.26** backend + **React** (Vite, TS, Tailwind, shadcn/ui) — Go 1.26.5, Node 24, Docker Compose v5 present locally |
| Schedule | No time limit → organised as milestones, full scope incl. innovation |
| AI | **AI-assisted**: Claude structures runbooks + drafts rules; validated, human-reviewed, heuristic fallback; opt-in per team |
| Domain | **Finance operations**: $-impact, cut-offs & market calendars, four-eyes, tamper-evident audit, FIX / ISO 20022 |

Rejected propositions: P2 microservices + Kafka Streams (too much surface for the gain), P3 Postgres+Redis only
(weak durability/scaling story); Java/Spring and TS stacks.

---

## Finance-sector focus

Positioning: *"Kardan — alerting that speaks money and deadlines"* for trading, settlement and payments ops.
Each feature plugs into the generic design below (marked §n) so flexibility is not sacrificed.

**Core (in scope):**
1. **Finance-aware schema detection** (§1) — on any unseen schema, auto-recognise ISIN (check digit), LEI,
   IBAN (mod-97), ISO 4217 currency codes, amounts, trade/order IDs → suggest facets `$instrument $counterparty
   $account $currency $notional $desk $book $tradeId $settlementDate` and auto-tag sensitive fields for masking.
2. **$-impact on every alert** (§2–3) — `$notionalUsd` via FX table (refreshed by the *polled API* source type,
   mocked), impact score = severity × exposure × urgency; feed sorts by money at risk, not only by time.
3. **Cut-off & market-calendar awareness** (§4) — calendars + cut-offs in `config/calendars.yaml` (illustrative
   values: Fedwire, CHAPS, T2, CLS window, exchange hours, holidays). Alerts show "⏱ 47 min to USD cut-off";
   escalation ladder tightens as the deadline approaches; market-hours-only rules (pricing latency outside trading
   hours is info, inside it is critical). Follow-the-sun on-call (London → New York → Hong Kong).
4. **New rule kinds** (§2) — besides per-event `match`: `aggregate` (windowed count/sum per key: "rejected
   notional per counterparty > 50M in 1h", "≥5 rejections for one counterparty in 10 min") and `absence`
   (expected event missing by a calendar-aware deadline: "custodian EOD positions file not received by 18:30 on a
   business day", "no price tick for an instrument for 30 s during market hours").
5. **Incident correlation by business key** (§3) — alerts sharing counterparty / instrument / workflow within a
   window fold into one incident with roll-up: "Counterparty X connectivity — 23 trades, $410M notional, nearest
   cut-off 16:45".
6. **Four-eyes (maker-checker)** (§5) — risky runbook actions (resubmit payment, pause trading / kill switch,
   failover feed) and changes to critical rules need approval by a *different* entitled person; enforced
   server-side.
7. **Tamper-evident audit trail** (§5) — append-only, hash-chained entries (each stores hash of previous) for
   acks, approvals, actions, rule changes; verify endpoint + exportable incident report (PDF/Markdown) to support
   audit and record-keeping needs.
8. **Channel-aware data protection** (§4) — field classification (`public/internal/confidential`); confidential
   fields (client names, IBANs) masked on SMS/phone/email (`****1234`), full only in UI for entitled users;
   entitlements per desk/team (mocked SSO).
9. **Finance message formats as sources** (§1) — FIX tag=value log parser (e.g. `35=8` ExecutionReport with
   `39=8` Rejected, `58` Text, `55` Symbol) and ISO 20022 XML (e.g. `pacs.002` payment status `RJCT` with reason
   codes like `AC04`, `AM04`) → JSON; shows schema-agnostic ingestion beyond JSON.
10. **Finance runbooks & actions seeded** (§5) — settlement fail, payment rejection (repair + resubmit, four-eyes),
    stale market data (fail over to secondary vendor feed), limit breach (notify risk, block desk — four-eyes),
    reconciliation break; actions `resubmit_payment`, `failover_feed`, `pause_trading`, `notify_risk` in mockops.
11. **"Market-open storm" load scenario** (Verification → Load) — steady load + 10× burst; report p50/p99 during the burst.

**Stretch:** regulatory incident clocks with configurable deadlines (e.g. DORA-style initial / intermediate /
final reports) + pre-filled report draft; mapping alerts to "important business services" with an
impact-tolerance countdown; AI incident summary in business language for desk heads.

**Demo story ("a day on the rates desk"):** market-open burst → counterparty service times out → settlement
failures + trade rejections correlate into one incident with $ impact and cut-off countdown → on-call phoned →
runbook auto-runs the health check and retry, escalation needs approval → a second person approves by SMS reply
(four-eyes) → resolved, audit chain verified, report exported → runbook update suggested. Then judges' unseen
schema → finance fields auto-detected → rule in ~30 s.

---

## Architecture

```
 HTTP webhook  POST /ingest/{sourceId} ─┐
 Kafka/Redpanda topic consumer          ├─► normalize ─► [events topic, Redpanda, N partitions]
 File tailer (pricing-engine-* logs)    │
 HTTP poller (optional)                ─┘
                                                │ consumer group = processor replicas
                                                ▼
     rules (compiled predicates on raw JSON) ─► dedup/group/throttle (Redis Lua)
                                                ▼
     alert upsert + notification outbox rows (Postgres, one TX) ─► publish alert-update (Redis pub/sub)
                       ▼                                                     ▼
     dispatcher replicas: claim outbox (SKIP LOCKED),             api replicas: REST + SSE live feed,
     prefs/on-call, channel adapters, retry, breaker, DLQ          embedded React UI, approvals
```

- Ingest answers `202` only after Redpanda acks the produce → no loss at the edge. Processor commits offsets only
  after the Postgres TX commits; idempotent on event ULID → at-least-once without duplicates.
- State: Postgres (rules, schemas, alerts, notifications, audit, runbooks), Redis (dedup windows, throttle
  buckets, group timers, pub/sub fan-out, rule-change broadcast), Redpanda (durable log + replay for backtest).
- In-process: one goroutine pipeline per assigned partition, bounded channels for backpressure, batched
  Postgres writes (pgx batch/`CopyFrom`), dedup counters coalesced and flushed every ~500 ms.

### Go libraries

| Concern | Library |
|---|---|
| HTTP / SSE | stdlib `net/http` (1.22+ routing), `http.Flusher` for SSE |
| Kafka | `github.com/twmb/franz-go` |
| Postgres | `github.com/jackc/pgx/v5`, `sqlc` (typed queries), `github.com/pressly/goose/v3` (migrations) |
| Redis | `github.com/redis/go-redis/v9` |
| Field access on raw JSON | `github.com/tidwall/gjson` (no unmarshal on the hot path) |
| Advanced rule expressions | `github.com/google/cel-go` (sandboxed CEL) |
| YAML / Markdown | `github.com/goccy/go-yaml`, `github.com/yuin/goldmark` (AST → steps, HTML for email) |
| File tail / hot reload | `github.com/nxadm/tail`, `github.com/fsnotify/fsnotify` |
| Resilience | `github.com/sony/gobreaker/v2` + own backoff w/ jitter |
| Email | `github.com/wneessen/go-mail` → Mailpit |
| Metrics | `github.com/prometheus/client_golang`, `github.com/HdrHistogram/hdrhistogram-go` |
| IDs / logs | `github.com/oklog/ulid/v2`, `log/slog` |
| LLM | `github.com/anthropics/anthropic-sdk-go/v2` |
| Tests | `testing`, `github.com/stretchr/testify`, `github.com/testcontainers/testcontainers-go` |
| UI | React + Vite + TS + Tailwind + shadcn/ui, TanStack Query, `react-querybuilder` (AND/OR builder), `@monaco-editor/react` (YAML tab) |
| Load | k6 (HTTP) + own `cmd/loadgen` (franz-go producer + HTTP) |

---

## Functional design

### 1. Ingestion & unknown schemas
- `Source` interface (`Start(ctx, emit func(Envelope)) error`): `httpSource`, `kafkaSource`, `fileTailSource`
  (regex/grok → JSON per line), `pollSource`. Declared in `config/sources.yaml` → new topic/log file = config only.
- `Envelope{ID ulid, SourceID, ReceivedAt, Payload json.RawMessage, Meta map}`.
- **Any field addressable by path** (`error.code`, `labels.severity`, `payload.notional`, `meta.schema`).
- **Canonical facets** `$severity $service $type $env $time` via heuristics (keys severity/level/priority,
  service/producer/app, type/alertname/schema…) with per-schema JSONPath overrides; severity normalised
  (`ERROR`, `critical`, `WARN`, `P1` → ordered levels) so "critical from payments-api" works across schemas.
- **Schema registry by signature** (hash of sorted key paths) storing sample + flattened fields. Unseen schema →
  UI toast "New schema detected" → "Create rule from this event" (click fields → conditions).

### 2. Rules (runtime-editable)
```yaml
id: rates-big-rejections
team: rates
match:
  all:
    - { field: meta.schema,      op: glob, value: "trade.rejected.*" }
    - { field: payload.desk,     op: eq,   value: rates }
    - { field: payload.notional, op: gt,   value: 10000000 }
routing:      [{ to: team:rates, channels: [email, ui] }]   # or `preferences` → recipient decides
presentation:
  title: "Rejected {{payload.currency}} {{payload.notional | number}} — {{payload.reason}}"
  fields: [payload.tradeId, meta.producer]
  severity: warning
  color: amber
dedup:       { key: "{{payload.tradeId}}", window: 10m }
group:       { by: [instrument], window: 5m }               # Team Pricing digest
runbooks:    [trade-limit-breach]
autoResolve: { when: { field: status, op: eq, value: resolved }, sameFingerprint: true }
```
- Condition tree `all/any/not`; ops `eq neq in nin gt gte lt lte contains startsWith glob regex exists
  severityAtLeast`, plus `expr` (CEL) for power users.
- Compiled once per version into `func(gjson.Result) bool` closures (pre-parsed paths, pre-built regex/glob) and
  swapped atomically (`atomic.Pointer[RuleSet]`). Optional index on `eq` discriminators for big rule sets.
- Sources of truth: Postgres via API/UI; `config/rules/*.yaml` loaded by fsnotify (`origin: file`, read-only in
  UI, "fork to UI" button). Any change → Redis `rules-changed` → all processors reload; periodic version check as
  safety net.
- Templates: small Mustache-style renderer over gjson paths with filters (`number`, `date`, `truncate`,
  `default`), HTML-escaped for UI/email; unresolved variables highlighted in preview.
- Appendix A/B rules and runbooks are seeded in `config/`.

### 3. Don't flood
- Fingerprint = hash(ruleID + rendered `dedup.key`; default `$type+$service+first(error.code, alertname, reason)`).
- Redis Lua: `SET NX PX window` → new alert, else `INCR` → ×N counter (500 identical errors = 1 alert ×500).
- Grouping: tumbling window per group key; due flushes in a Redis ZSET claimed atomically by any dispatcher
  (no leader) → one digest. Throttle: token bucket per recipient+channel; overflow folds into next digest.

### 4. Delivery
- `Channel` interface (`Name()`, `Render(alert, recipient)`, `Send(ctx, msg)`): `email` (SMTP → Mailpit), `sms`,
  `phone` (→ fake gateway HTTP), `ui` (SSE). Real provider (Twilio/SES) = one new file + config.
- Preferences per person/team: severity → channels with time windows + timezone (Anna: critical → SMS 08–20,
  else email), quiet hours, weekly on-call rotation + overrides (`config/teams.yaml` + UI). Clock injectable
  for the demo.
- Per-channel rendering: UI/email = full alert + runbook; SMS = title + short link `/a/{shortId}`; phone = TTS
  summary + "press 1 to ack".
- Outbox in Postgres: dispatchers claim `FOR UPDATE SKIP LOCKED LIMIT n`, woken by `LISTEN/NOTIFY`; exponential
  backoff + jitter, gobreaker per channel, fallback channel after N failures, DLQ with re-send in UI.
- **`cmd/fakegw`**: phone-simulator page (SMS bubbles, incoming call with browser TTS, reply box) + failure
  injection (error rate / latency / down) + receipt log used by the load test.

### 5. Runbooks & actions
- `Parser` interface → common model `Runbook{Matches, Steps[]}`, step kind `text | check | action`, fields
  `approval: none|required`, optional `if`. Parsers: YAML (Appendix B), Markdown (goldmark AST: headings,
  ordered/checkbox lists), checklist, link (URL + optional snapshot), free text (sentence split + verb
  heuristics) — and the **Claude structurer** when enabled (§7). Parsed preview is editable before saving.
- Attachment by rule id, schema signature, or a `matches:` condition (same DSL) → a judge's new runbook only
  needs a match condition.
- `Action` interface (`Type()`, `Risk()`, `ParamsSchema()`, `Execute(ctx, params)`): `http_get`, `call_webhook`,
  `retry_workflow`, `restart_service`, `open_ticket`, `escalate`, `notify_channel`, `wait`. Targets in
  **`cmd/mockops`**. Host allowlist (SSRF guard).
- Approvals: risky steps → `PENDING_APPROVAL`; approve in UI, SMS reply `APPROVE 7F3K`, or signed one-time email
  link (HMAC). Every step/result/actor goes to the alert's audit timeline.
- Auto-close: resolved event with same fingerprint, silence timeout, or a passing `check` step.
- "Propose fixes / enhancements": anyone suggests a change to rule/presentation/runbook from the alert card →
  proposal queue, owner accepts/rejects (PR-like). System suggestions (§7, fatigue stats) use the same queue.

### 6. UI (React, embedded in the binary via `//go:embed`)
- **Live feed** per team (SSE; api replicas subscribe to Redis pub/sub so any replica can push): colour per rule,
  `×N` badge, ack/resolve, runbook panel with action/approve buttons, timeline.
- **Rule editor:** react-querybuilder with field autocomplete from seen schemas, YAML tab, **dry-run against recent
  events** ("would have matched 132× in the last hour") + rendered alert preview, plain-English box (§7).
- Schemas · Runbooks (paste any format → parsed preview) · Preferences (severity × channel matrix, hours, on-call)
  · Deliveries (status, retries, DLQ) · Proposals · Stats (msgs/s, p50/p99, noisiest rules).
- **Onboarding wizard** (team → members → paste sample event → pick fields → channels), target < 2 min.

### 7. AI assistance (Claude)
- `internal/ai`: `Structurer` interface; `heuristic` impl always on, `claude` impl when credentials exist
  (`anthropic.NewClient()` resolves `ANTHROPIC_API_KEY` or an `ant auth login` profile).
- Uses: (a) free-text/Markdown/link-snapshot runbook → structured steps with template variables
  (`{{instance}}`) and action types from our catalog; (b) plain-English → rule draft; (c) stretch: resolution
  timeline → suggested runbook diff into the proposal queue.
- Model `claude-opus-5-5`, effort set explicitly (`medium`), server-side refusal fallbacks enabled
  (`fallbacks: "default"`, beta `server-side-fallback-2026-07-01`); check `stop_reason` before reading content.
  Stable system prompt + action catalog marked for prompt caching.
- Output via **structured outputs** (`output_config.format`, JSON schema). The Go binding for it is not in the
  bundled SDK docs → verify against the `anthropic-sdk-go` repo when implementing; fallback is a `strict: true`
  tool with `tool_choice: auto` (forced tool choice returns 400 on Opus 5.5).
- Structured outputs reject **recursive** schemas → the rule-draft schema is a depth-limited tree (group →
  groups → leaves), then compiled by our rule compiler, field names checked against the schema registry, and
  dry-run shown before a human saves.
- Security & privacy: only runbook text or the user's sentence + field *names* are sent (never event values);
  regex redaction of emails/phones/tokens; LLM output is data — schema-validated, action types allowlisted,
  risky types forced to `approval: required`, nothing executes without human review → prompt injection in a
  runbook can't trigger actions; feature flag + offline heuristic fallback. Documented in `docs/ai-usage.md`
  together with how AI tools were used during development.

---

## Repo layout

```
kardan-alerts/
  docker-compose.yml   # redpanda, postgres, redis, mailpit, fakegw, mockops, kardan (scale N), prometheus, grafana
  Makefile             # make demo | test | load | seed
  go.mod
  cmd/kardan/          # main binary; ROLES env selects ingest/processor/dispatcher/api
  cmd/fakegw/          # SMS/phone simulator + failure injection + receipt log
  cmd/mockops/         # workflow service, counterparty health, ticketing (action targets)
  cmd/loadgen/         # HTTP + Kafka load generator, prints msgs/s and latency percentiles
  internal/ingest/     # Source interface, http/kafka/filetail/poll; parsers: json, regex/grok, fix, iso20022 (xml)
  internal/schema/     # facets, signature registry, severity normalisation, finance field detectors (ISIN/LEI/IBAN/ccy)
  internal/finance/    # FX table, calendars & cut-offs, impact score, incident correlation, masking/classification
  internal/rules/      # model, compiler, engine (match / aggregate / absence), store, reload
  internal/tmpl/       # template renderer
  internal/alerting/   # fingerprint, dedup/group/throttle (Lua), alerts, autoresolve
  internal/notify/     # Channel interface, email/sms/phone/ui, prefs & on-call, outbox, dispatcher
  internal/runbooks/   # Parser interface, yaml/markdown/checklist/link/freetext, render
  internal/actions/    # Action interface, handlers, four-eyes approvals, hash-chained audit
  internal/ai/         # Structurer interface, heuristic + claude
  internal/api/        # REST, SSE hub, embedded UI
  internal/store/      # sqlc queries + goose migrations
  internal/platform/   # config, kafka, redis, pg, metrics, roles
  web/                 # React app (built into internal/api/dist)
  config/              # sources.yaml, teams.yaml, calendars.yaml, fx.yaml, classification.yaml, rules/*.yaml, runbooks/*
  samples/             # Appendix A events, FIX logs, ISO 20022 pacs.002 XML, scenario scripts (rates-desk day)
  loadtest/            # k6 script, loadgen scenarios, results.md
  docs/                # architecture.md, mocked.md, ai-usage.md
```

---

## Implementation notes (decided while building M1–M2)

- **Dedup without Redis Lua:** the Postgres partial unique index (one unresolved alert per fingerprint) plus
  per-batch folding in the processor gives the same guarantees with one fewer hop; Redis is used for live fan-out
  (and later throttling, group timers, aggregate windows). `dedup.window` will drive re-notify / silence
  auto-resolve in M5.
- **Plain pgx instead of sqlc:** the write path uses pgx batches + `CopyFrom`; no codegen step for judges.
- Dispatcher claims only as many outbox rows as it has free workers (no lease expiry → no double sends).

## Implementation notes (M3)

- **Config hot reload by polling, not fsnotify:** file-system events from a Windows/macOS host do not reach
  containers through Docker Desktop bind mounts, so the config dir is fingerprinted every 2 s; API changes travel
  over Redis pub/sub, with a 15 s DB version check as a safety net.
- **Parsing happens at ingest**, so the events topic carries JSON payloads plus the original text (`raw`) and
  `format`; alerts keep the first event's raw text so the rule editor replays it through the same parser.
- **Stored rules override file rules with the same id**: editing a file rule in the UI never rewrites the file.
- **Schema auto-facets are recomputed on read** from stored fields, so detection improvements apply to old schemas.
- File sources are leased per source in Redis (one tailing replica, fail-over within the lease TTL).

## Milestones

1. **Skeleton** — go.mod, compose (redpanda, postgres, redis, mailpit), role switch, migrations, React scaffold
   embedded, `make demo` works.
2. **Vertical slice** — HTTP ingest → rule engine → dedup → alert in Postgres → SSE live feed → email via Mailpit.
3. **Flexibility** — Kafka + file-tail sources, FIX + ISO 20022 parsers, schema registry + facets + finance field
   detectors, rules CRUD + fsnotify reload, templates, presentation, Appendix rules seeded.
4. **Delivery & reliability** — fakegw (SMS/phone), preferences + time windows + follow-the-sun on-call, field
   classification + masking, outbox, retries, breaker, fallback, DLQ, failure-injection demo.
5. **Noise control & money** — grouping digests, throttling, ack/resolve, auto-resolve, FX + $-impact score,
   incident correlation by counterparty/instrument/workflow.
6. **Time-aware rules** — `aggregate` + `absence` rule kinds, calendars & cut-offs, cut-off countdown escalation.
7. **Runbooks & actions** — parsers, per-channel rendering, finance action handlers + mockops, four-eyes approvals
   (UI/SMS/link), hash-chained audit + verify + incident report export.
8. **UI depth** — rule builder + dry-run, schemas page (finance fields highlighted), impact-sorted feed, incident
   view, preferences matrix, deliveries, onboarding wizard.
9. **AI** — Claude structurer + rule drafting (opt-in per team), proposal queue, resolution-learned suggestions.
10. **Measure & scale** — Prometheus/Grafana, loadgen + k6 incl. market-open storm, 1 vs 3 replicas, `results.md`.
11. **Polish** — README (clone → demo in 1–2 commands), "rates desk day" scenario script, architecture diagram,
    mocked.md, ai-usage.md, video script. Stretch: regulatory clocks, business-service impact tolerance.

---

## Verification

- `make demo` (= `docker compose up --build` + seed) → UI, Mailpit, phone simulator, Grafana reachable.
- `samples/send-all` sends Appendix A over HTTP + Kafka + log lines; expected: Settlements → red card with
  step + error, phone + SMS in simulator; Rates → templated title email in Mailpit; Pricing → one digest per
  instrument per 5 min; Anna → SMS vs email depending on (injected) clock.
- Same event ×500 → one alert, count 500.
- Stop `fakegw` mid-burst, restart → delivered count equals expected (no loss); DLQ only after max attempts.
- Edit `config/rules/*.yaml` and `PUT /api/rules/{id}` → effect without restart on every replica.
- POST a never-seen schema → appears in Schemas → rule built from it in the UI fires.
- Paste an unseen free-text runbook → parsed steps (heuristic and Claude) → shown on matching alert with values.
- Finance: unseen JSON with ISIN/IBAN/LEI fields → detected + masked on SMS; FIX reject line and pacs.002 `RJCT`
  XML each produce an alert; `absence` rule fires when the EOD file is missing after the deadline (fake clock) and
  stays silent on a holiday; `aggregate` rule fires on the 5th rejection for one counterparty; correlated incident
  shows summed USD notional; approver == requester is refused (four-eyes); tampering with one audit row makes
  `GET /api/audit/verify` fail at that entry.
- `go test ./...`: unit tests for condition compiler, field paths, templates, facets, parsers, preference/time
  windows, retry policy, Lua scripts; integration tests with testcontainers (Redpanda, Postgres, Redis) for
  ingest → delivery; `go test -bench` for rule evaluation.
- Load: `make load` runs loadgen/k6 with unique fingerprints (worst case) and a realistic mix; reports msgs/s and
  p50/p99 measured both server-side (Prometheus histogram ingest→send) and externally (fakegw receipt time −
  `sentAt` in payload), for 1 and 3 replicas → `loadtest/results.md`.
