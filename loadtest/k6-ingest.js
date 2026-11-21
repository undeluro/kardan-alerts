// k6 check of the HTTP ingest edge: how fast Kardan durably accepts events.
// End-to-end latency and loss are measured by cmd/loadgen; this script is an
// independent, tool-standard view of the ingest side only.
//
//   docker run --rm -i --network kardan_default -e KARDAN_URL=http://api:8080 \
//     grafana/k6:0.57.0 run - < loadtest/k6-ingest.js
//
// Events go to source "k6" and match no rule, so nothing is notified.
import http from "k6/http";
import { check } from "k6";

const base = __ENV.KARDAN_URL || "http://localhost:8080";
const batch = Number(__ENV.BATCH || 20);

export const options = {
  scenarios: {
    steady: { executor: "constant-arrival-rate", rate: 50, timeUnit: "1s", duration: "30s", preAllocatedVUs: 20, maxVUs: 100 },
    open: { executor: "constant-arrival-rate", rate: 250, timeUnit: "1s", duration: "15s", startTime: "30s", preAllocatedVUs: 50, maxVUs: 300 },
  },
  thresholds: {
    http_req_failed: ["rate<0.001"],
    "http_req_duration{scenario:steady}": ["p(99)<250"],
  },
};

export default function () {
  const now = new Date().toISOString();
  const lines = [];
  for (let i = 0; i < batch; i++) {
    lines.push(JSON.stringify({ type: "K6_PROBE", service: "k6", severity: "info", vu: __VU, iter: __ITER, i, at: now }));
  }
  const res = http.post(`${base}/ingest/k6/batch`, lines.join("\n"), { headers: { "Content-Type": "application/x-ndjson" } });
  check(res, { "accepted (202)": (r) => r.status === 202 });
}
