loadgen run tou16s: steady, http, 100 ev/s for 15s
  warm-up delivered
  steady                 100 ev/s for 15s
  sent 1500 events (100/s); waiting for 1500 deliveries…

### settle after scaling (not recorded)

| | |
|---|---|
| Scenario | steady over http |
| Worker replicas | 3 |
| Target / achieved send rate | 100 / 100 events/s |
| Events sent (refused) | 1500 (0) |
| Ingest ack p50 / p99 | 1.3 / 9.3 ms per batch |
| Pipeline throughput | 100 events/s (send start to last delivery) |
| Alert SMS expected / delivered / lost | 1500 / 1500 / 0 |
| Server-side e2e p50 / p95 / p99 (Prometheus) | 18 / 41 / 48 ms |

End to end (event accepted -> SMS accepted by provider, measured at the gateway):

| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| steady | 100 | 1500 | 17 | 29 | 31 | 33 |

  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)
