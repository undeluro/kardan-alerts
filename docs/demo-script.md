# Five-minute demo script

Setup: `docker compose up --build`, wait for the UI at :8080, open three tabs (Kardan :8080, phone simulator
:8090, Mailpit :8025). Set *Acting as* to `Priya`. Run `./samples/rates-desk-day.sh` and press Enter on cue.

| Time | Show | Say |
|---|---|---|
| 0:00 | Alert queue, empty | "Kardan is alerting for trading, settlement and payments operations. It takes events of any shape from any source and gets them to the right people, on their channel, without flooding them." |
| 0:20 | Step "Market open": pricing digest | "A burst of pricing warnings from a log file becomes one digest per instrument per five minutes." |
| 0:45 | Step "Settlements fail": incident card ACME-CAPITAL, money at risk, cut-off chip | "A settlement timeout, trade rejections from Kafka and FIX rejects from a log file correlate into one incident with the dollars at risk and the time left to the next payment cut-off. The queue is ranked by money and urgency, not by time." |
| 1:20 | Phone simulator: call and SMS; alert sheet runbook panel | "The on-call is phoned and texted, masked for the channel. The runbook started itself: health check failed, retry failed twice, so it wants to escalate, and that needs a second person." |
| 1:50 | Approve from the phone simulator (`APPROVE <code>` as Maria) | "Four eyes, enforced by the server: the requester cannot approve, and a high-risk step needs someone from the team. Approval by SMS reply, email link or the UI." |
| 2:15 | Step "Recovers"; Audit page, *Verify chain*; export report | "Everything is in a hash-chained audit log; change one row and verification points at it. The incident report is one click." |
| 2:40 | Proposals: fatigue suggestion with diff | "Kardan learns from how alerts were handled: these cleared by themselves, so it proposes a change. Someone else reviews the diff and applies it, like a pull request." |
| 3:05 | Bonus step: "New schema detected" toast, Schemas page | "Now a system we have never seen: margin calls from a collateral hub. LEI, ISIN, amount and currency are recognised on arrival." |
| 3:25 | Teams → Onboard a team: paste the event, pick conditions, channels, review (test + backtest), create, send sample | "From a sample event to a live rule for a new team in under two minutes, no code. The review step tests the rule on the sample and backtests it on the last 24 hours of real traffic." |
| 4:15 | Rule editor: builder ↔ YAML, *Describe the rule* | "Rules are data: a visual builder and YAML stay in sync, and an optional Claude assistant drafts rules from a sentence, always reviewed, with an offline fallback." |
| 4:35 | Grafana dashboard, `loadtest/results.md` | "One binary scaled by roles. We measured it: throughput, p50/p99 end to end, and zero lost notifications with one and three workers." |
| 4:55 | End | "Kardan: alerting that speaks money and deadlines." |
