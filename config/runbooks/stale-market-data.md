---
# A checklist for the "market data feed silent" heartbeat rule.
runbook: stale-market-data
team: pricing
rules: [market-data-stale]
---
# Stale market data
- [ ] Check the vendor status page for {{instrument}}
- [ ] Fail {{instrument}} over to the secondary vendor feed
- [ ] Tell the trading desks that {{instrument}} quotes may be stale and post in #market-data
