loadgen run tou04h: steady, http, 200 ev/s for 30s
  warm-up delivered
  steady                 200 ev/s for 30s
  sent 6000 events (200/s); waiting for 6000 deliveries…

### Steady 200 ev/s, every event a new alert (1 worker)

| | |
|---|---|
| Scenario | steady over http |
| Worker replicas | 1 |
| Target / achieved send rate | 200 / 200 events/s |
| Events sent (refused) | 6000 (0) |
| Ingest ack p50 / p99 | 1.4 / 31.6 ms per batch |
| Pipeline throughput | 200 events/s (send start to last delivery) |
| Alert SMS expected / delivered / lost | 6000 / 6000 / 0 |
| Server-side e2e p50 / p95 / p99 (Prometheus) | 19 / 49 / 93 ms |

End to end (event accepted -> SMS accepted by provider, measured at the gateway):

| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| steady | 200 | 6000 | 17 | 48 | 87 | 132 |

  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)
loadgen run tou06m: steady, http, 500 ev/s for 30s
  warm-up delivered
  steady                 500 ev/s for 30s
  sent 15000 events (500/s); waiting for 15000 deliveries…

### Steady 500 ev/s, every event a new alert (1 worker)

| | |
|---|---|
| Scenario | steady over http |
| Worker replicas | 1 |
| Target / achieved send rate | 500 / 500 events/s |
| Events sent (refused) | 15000 (0) |
| Ingest ack p50 / p99 | 0.9 / 21.3 ms per batch |
| Pipeline throughput | 495 events/s (send start to last delivery) |
| Alert SMS expected / delivered / lost | 15000 / 15000 / 0 |
| Server-side e2e p50 / p95 / p99 (Prometheus) | 14 / 72 / 203 ms |

End to end (event accepted -> SMS accepted by provider, measured at the gateway):

| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| steady | 500 | 15000 | 12 | 63 | 159 | 332 |

  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)
loadgen run tou08p: storm, http, 100 ev/s for 1m0s
  warm-up delivered
  before open            100 ev/s for 20s
  market open (10x)     1000 ev/s for 20s
  after open             100 ev/s for 20s
  sent 24000 events (400/s); waiting for 24000 deliveries…

### Market-open storm 100 -> 1000 -> 100 ev/s (1 worker), run 1

| | |
|---|---|
| Scenario | storm over http |
| Worker replicas | 1 |
| Target / achieved send rate | 100 / 400 events/s |
| Events sent (refused) | 24000 (0) |
| Ingest ack p50 / p99 | 1.3 / 67.1 ms per batch |
| Pipeline throughput | 400 events/s (send start to last delivery) |
| Alert SMS expected / delivered / lost | 24000 / 24000 / 0 |
| Server-side e2e p50 / p95 / p99 (Prometheus) | 22 / 1900 / 2380 ms |

End to end (event accepted -> SMS accepted by provider, measured at the gateway):

| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| before open | 100 | 2000 | 17 | 46 | 75 | 99 |
| market open (10x) | 1000 | 20000 | 18 | 1661 | 2010 | 2477 |
| after open | 100 | 2000 | 23 | 219 | 786 | 804 |

  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)
loadgen run tou0c8: storm, http, 100 ev/s for 1m0s
  warm-up delivered
  before open            100 ev/s for 20s
  market open (10x)     1000 ev/s for 20s
  after open             100 ev/s for 20s
  sent 24000 events (400/s); waiting for 24000 deliveries…

### Market-open storm 100 -> 1000 -> 100 ev/s (1 worker), run 2

| | |
|---|---|
| Scenario | storm over http |
| Worker replicas | 1 |
| Target / achieved send rate | 100 / 400 events/s |
| Events sent (refused) | 24000 (0) |
| Ingest ack p50 / p99 | 1.3 / 105.8 ms per batch |
| Pipeline throughput | 400 events/s (send start to last delivery) |
| Alert SMS expected / delivered / lost | 24000 / 24000 / 0 |
| Server-side e2e p50 / p95 / p99 (Prometheus) | 22 / 979 / 2187 ms |

End to end (event accepted -> SMS accepted by provider, measured at the gateway):

| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| before open | 100 | 2000 | 24 | 315 | 1026 | 1086 |
| market open (10x) | 1000 | 20000 | 17 | 1023 | 1259 | 1512 |
| after open | 100 | 2000 | 20 | 57 | 128 | 139 |

  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)
loadgen run tou0fn: realistic, http, 1000 ev/s for 30s
  warm-up delivered
  realistic             1000 ev/s for 30s
  sent 30000 events (1000/s); waiting for 3450 deliveries…

### Realistic mix 1000 ev/s, 10% new alerts (1 worker)

| | |
|---|---|
| Scenario | realistic over http |
| Worker replicas | 1 |
| Target / achieved send rate | 1000 / 1000 events/s |
| Events sent (refused) | 30000 (0) |
| Ingest ack p50 / p99 | 0.9 / 2001.5 ms per batch |
| Pipeline throughput | 1000 events/s (send start to last delivery) |
| Alert SMS expected / delivered / lost | 3450 / 3450 / 0 |
| Server-side e2e p50 / p95 / p99 (Prometheus) | 10 / 414 / 1823 ms |

End to end (event accepted -> SMS accepted by provider, measured at the gateway):

| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| realistic | 1000 | 3450 | 9 | 378 | 2143 | 2804 |

  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)
