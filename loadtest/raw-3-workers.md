loadgen run tou18t: steady, http, 200 ev/s for 30s
  warm-up delivered
  steady                 200 ev/s for 30s
  sent 6000 events (200/s); waiting for 6000 deliveries…

### Steady 200 ev/s, every event a new alert (3 workers)

| | |
|---|---|
| Scenario | steady over http |
| Worker replicas | 3 |
| Target / achieved send rate | 200 / 200 events/s |
| Events sent (refused) | 6000 (0) |
| Ingest ack p50 / p99 | 1.8 / 21.6 ms per batch |
| Pipeline throughput | 200 events/s (send start to last delivery) |
| Alert SMS expected / delivered / lost | 6000 / 6000 / 0 |
| Server-side e2e p50 / p95 / p99 (Prometheus) | 23 / 82 / 146 ms |

End to end (event accepted -> SMS accepted by provider, measured at the gateway):

| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| steady | 200 | 6000 | 23 | 66 | 117 | 168 |

  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)
loadgen run tou1aj: steady, http, 500 ev/s for 30s
  warm-up delivered
  steady                 500 ev/s for 30s
  sent 15000 events (500/s); waiting for 15000 deliveries…

### Steady 500 ev/s, every event a new alert (3 workers)

| | |
|---|---|
| Scenario | steady over http |
| Worker replicas | 3 |
| Target / achieved send rate | 500 / 500 events/s |
| Events sent (refused) | 15000 (0) |
| Ingest ack p50 / p99 | 0.8 / 9.6 ms per batch |
| Pipeline throughput | 500 events/s (send start to last delivery) |
| Alert SMS expected / delivered / lost | 15000 / 15000 / 0 |
| Server-side e2e p50 / p95 / p99 (Prometheus) | 15 / 34 / 49 ms |

End to end (event accepted -> SMS accepted by provider, measured at the gateway):

| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| steady | 500 | 15000 | 11 | 27 | 42 | 100 |

  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)
loadgen run tou1cd: storm, http, 100 ev/s for 1m0s
  warm-up delivered
  before open            100 ev/s for 20s
  market open (10x)     1000 ev/s for 20s
  after open             100 ev/s for 20s
  sent 24000 events (400/s); waiting for 24000 deliveries…

### Market-open storm 100 -> 1000 -> 100 ev/s (3 workers), run 1

| | |
|---|---|
| Scenario | storm over http |
| Worker replicas | 3 |
| Target / achieved send rate | 100 / 400 events/s |
| Events sent (refused) | 24000 (0) |
| Ingest ack p50 / p99 | 2.3 / 3374.3 ms per batch |
| Pipeline throughput | 400 events/s (send start to last delivery) |
| Alert SMS expected / delivered / lost | 24000 / 24000 / 0 |
| Server-side e2e p50 / p95 / p99 (Prometheus) | 1601 / 4665 / 4969 ms |

End to end (event accepted -> SMS accepted by provider, measured at the gateway):

| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| before open | 100 | 2000 | 23 | 53 | 84 | 93 |
| market open (10x) | 1000 | 20000 | 2026 | 4703 | 4938 | 5239 |
| after open | 100 | 2000 | 17 | 134 | 912 | 923 |

  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)
loadgen run tou1f6: storm, http, 100 ev/s for 1m0s
  warm-up delivered
  before open            100 ev/s for 20s
  market open (10x)     1000 ev/s for 20s
  after open             100 ev/s for 20s
  sent 24000 events (400/s); waiting for 24000 deliveries…

### Market-open storm 100 -> 1000 -> 100 ev/s (3 workers), run 2

| | |
|---|---|
| Scenario | storm over http |
| Worker replicas | 3 |
| Target / achieved send rate | 100 / 400 events/s |
| Events sent (refused) | 24000 (0) |
| Ingest ack p50 / p99 | 1.2 / 118.0 ms per batch |
| Pipeline throughput | 400 events/s (send start to last delivery) |
| Alert SMS expected / delivered / lost | 24000 / 24000 / 0 |
| Server-side e2e p50 / p95 / p99 (Prometheus) | 18 / 600 / 973 ms |

End to end (event accepted -> SMS accepted by provider, measured at the gateway):

| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| before open | 100 | 2000 | 15 | 22 | 43 | 55 |
| market open (10x) | 1000 | 20000 | 13 | 499 | 666 | 815 |
| after open | 100 | 2000 | 44 | 2382 | 3799 | 3818 |

  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)
loadgen run tou1i5: realistic, http, 1000 ev/s for 30s
  warm-up delivered
  realistic             1000 ev/s for 30s
  sent 30000 events (1000/s); waiting for 3450 deliveries…

### Realistic mix 1000 ev/s, 10% new alerts (3 workers)

| | |
|---|---|
| Scenario | realistic over http |
| Worker replicas | 3 |
| Target / achieved send rate | 1000 / 1000 events/s |
| Events sent (refused) | 30000 (0) |
| Ingest ack p50 / p99 | 1.0 / 21.6 ms per batch |
| Pipeline throughput | 992 events/s (send start to last delivery) |
| Alert SMS expected / delivered / lost | 3450 / 3450 / 0 |
| Server-side e2e p50 / p95 / p99 (Prometheus) | 8 / 39 / 84 ms |

End to end (event accepted -> SMS accepted by provider, measured at the gateway):

| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| realistic | 1000 | 3450 | 8 | 90 | 126 | 160 |

  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)
