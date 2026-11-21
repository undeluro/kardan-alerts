# What is real and what is mocked

Everything Kardan itself does is real and runs in the compose stack: ingestion from HTTP, Kafka and files,
parsing, rules, deduplication, windows, incidents, delivery with retries and breakers, runbooks with approvals,
the audit chain, the UI and the measurements in `loadtest/results.md`. What is simulated is the outside world
Kardan talks to.

| Real system | In this repository | What it takes to make it real |
|---|---|---|
| SMTP provider | **Mailpit** (real SMTP server that captures mail, UI on :8025) | Point `SMTP_ADDR` at the corporate relay |
| SMS / voice provider (Twilio, Vonage…) | **`cmd/fakegw`**: the same contract as a real provider (HTTP send API, idempotency keys, inbound reply and keypad webhooks) with a phone simulator, failure injection (down, flaky, slow) and delivery statistics | One more `notify.Channel` implementation and credentials |
| Kafka | **Redpanda**, single node (Kafka API compatible) | `KAFKA_BROKERS`; nothing else changes |
| Operations systems that runbooks act on (workflow engine, service manager, ticketing, chat, risk, market-data feeds, regions, deployments, payments hub, trading controls) | **`cmd/mockops`**, with idempotency, a state page and switches to break `counterparty-service` | Entries in `config/actions.yaml` pointing at the real endpoints (host allow-list applies) |
| Market and payment calendars, cut-offs | `config/calendars.yaml` with illustrative hours, cut-offs and 2026 holidays | Load from a reference-data service |
| FX rates | `config/fx.yaml`, static | Feed from a rates source (the file reloads live) |
| Identity / SSO | An "Acting as" name in the UI mapped to people in the directory; approvals, entitlements and four-eyes checks run on the server against that name. SMS replies are identified by phone number, email approval links by HMAC signature | Take identity from SSO (OIDC headers or tokens) instead of the free-text name |
| Claude | Real API when `ANTHROPIC_API_KEY` is set; otherwise the built-in readers | Set the key and opt teams in |
| Business clock | Real clock, with a shared offset in demo mode so schedules and cut-offs can be shown at "21:00" or "next week" | Turn off `DEMO_MODE` |

Data in `config/` and `samples/` is invented: people, phone numbers (reserved fictional ranges), counterparties,
workflow ids and amounts. Identifier examples such as ISINs, LEIs and IBANs use valid check digits so the
detectors can be demonstrated, and do not refer to real positions or accounts.
