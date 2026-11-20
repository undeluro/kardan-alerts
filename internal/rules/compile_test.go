package rules

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

// Appendix A sample events.
const workflowFailed = `{
  "type": "WORKFLOW_FAILED", "workflowId": "WF-20261023-00042", "workflowName": "order-settlement",
  "step": "validate-counterparty", "severity": "ERROR", "environment": "prod",
  "timestamp": "2026-10-23T09:14:02.331Z",
  "error": {"code": "TIMEOUT", "message": "Counterparty service did not respond in 5000 ms"}}`

const metricBreach = `{
  "alertname": "HighErrorRate",
  "labels": {"service": "payments-api", "region": "eu-west", "severity": "critical"},
  "annotations": {"summary": "Error rate 12% over the last 5 minutes"},
  "startsAt": "2026-10-23T09:13:00Z", "status": "firing"}`

const tradeRejected = `{
  "meta": {"schema": "trade.rejected.v2", "producer": "trade-gateway", "ts": 1792833245000},
  "payload": {"tradeId": "T-998877", "desk": "rates", "notional": 25000000, "currency": "EUR",
              "reason": "LIMIT_BREACH"}}`

func doc(t *testing.T, payload string) *schema.Doc {
	t.Helper()
	if !json.Valid([]byte(payload)) {
		t.Fatalf("invalid test JSON: %s", payload)
	}
	return schema.NewDoc(&event.Envelope{ID: "e1", SourceID: "test", ReceivedAt: time.Now(),
		Payload: json.RawMessage(payload)})
}

func mustCompile(t *testing.T, r Rule) *Compiled {
	t.Helper()
	if r.ID == "" {
		r.ID = "t"
	}
	if r.Team == "" {
		r.Team = "team"
	}
	c, err := Compile(r, time.Minute)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return c
}

func leaf(field, op string, value any) Condition {
	return Condition{Field: field, Op: op, Value: value}
}

func TestAppendixRules(t *testing.T) {
	settlements := mustCompile(t, Rule{Match: Condition{All: []Condition{
		leaf("type", "eq", "WORKFLOW_FAILED"),
		leaf("workflowName", "eq", "order-settlement"),
		leaf("environment", "eq", "prod"),
	}}})
	rates := mustCompile(t, Rule{
		Match: Condition{All: []Condition{
			leaf("meta.schema", "glob", "trade.rejected.*"),
			leaf("payload.desk", "eq", "rates"),
			leaf("payload.notional", "gt", 10_000_000),
		}},
		Presentation: Presentation{Title: "Rejected {{payload.currency}} {{payload.notional | number}} — {{payload.reason}}"},
	})
	anna := mustCompile(t, Rule{Match: Condition{All: []Condition{
		leaf("$severity", "eq", "critical"),
		leaf("$service", "eq", "payments-api"),
	}}})

	cases := []struct {
		name  string
		rule  *Compiled
		event string
		want  bool
	}{
		{"settlements/workflow", settlements, workflowFailed, true},
		{"settlements/metric", settlements, metricBreach, false},
		{"rates/trade", rates, tradeRejected, true},
		{"rates/workflow", rates, workflowFailed, false},
		{"anna/metric", anna, metricBreach, true},
		{"anna/workflow", anna, workflowFailed, false},
	}
	for _, tc := range cases {
		if got := tc.rule.Matches(doc(t, tc.event)); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}

	r := rates.Render(doc(t, tradeRejected))
	if want := "Rejected EUR 25,000,000 — LIMIT_BREACH"; r.Title != want {
		t.Errorf("title = %q, want %q", r.Title, want)
	}
}

func TestOperators(t *testing.T) {
	ev := `{"amount": 1500000, "code": "REJECTED", "tags": ["fx", "urgent"], "name": "pricing-engine-7",
	        "level": "WARN", "flag": true, "num_str": "42.5"}`
	cases := []struct {
		cond Condition
		want bool
	}{
		{leaf("amount", "gt", 1_000_000), true},
		{leaf("amount", "lte", 1_000_000), false},
		{leaf("num_str", "gte", 42), true},
		{leaf("code", "in", []any{"TIMEOUT", "REJECTED"}), true},
		{leaf("code", "nin", []any{"TIMEOUT", "REJECTED"}), false},
		{leaf("missing", "nin", []any{"X"}), true},
		{leaf("missing", "neq", "X"), true},
		{leaf("missing", "eq", ""), false},
		{leaf("tags", "contains", "urgent"), true},
		{leaf("tags", "eq", "fx"), true},
		{leaf("code", "contains", "eject"), true},
		{leaf("name", "glob", "pricing-engine-*"), true},
		{leaf("name", "glob", "PRICING-ENGINE-?"), true},
		{leaf("name", "regex", `-\d+$`), true},
		{leaf("name", "startsWith", "pricing"), true},
		{leaf("amount", "exists", nil), true},
		{leaf("nope", "exists", false), true},
		{leaf("level", "severityAtLeast", "warning"), true},
		{leaf("level", "severityAtLeast", "error"), false},
		{leaf("$severity", "eq", "warning"), true},
		{leaf("flag", "eq", true), true},
		{Condition{Not: &Condition{Field: "code", Op: "eq", Value: "REJECTED"}}, false},
		{Condition{Any: []Condition{leaf("code", "eq", "X"), leaf("amount", "gt", 1)}}, true},
	}
	for _, tc := range cases {
		c := mustCompile(t, Rule{Match: tc.cond})
		if got := c.Matches(doc(t, ev)); got != tc.want {
			t.Errorf("%+v: got %v, want %v", tc.cond, got, tc.want)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	bad := []struct {
		rule Rule
		want string
	}{
		{Rule{ID: "a", Team: "t"}, "empty condition"},
		{Rule{ID: "a", Team: "t", Match: leaf("x", "bogus", 1)}, "unknown op"},
		{Rule{ID: "a", Team: "t", Match: leaf("x", "gt", "abc")}, "must be a number"},
		{Rule{ID: "a", Team: "t", Match: leaf("x", "in", "notalist")}, "must be a list"},
		{Rule{ID: "a", Team: "t", Match: leaf("x", "regex", "(")}, "invalid regex"},
		{Rule{ID: "a", Match: leaf("x", "eq", 1)}, "team is required"},
		{Rule{ID: "a", Team: "t", Match: leaf("x", "eq", 1), Routing: []Route{{To: "rates", Channels: []string{"email"}}}}, "must start with team:"},
		{Rule{ID: "a", Team: "t", Match: leaf("x", "eq", 1), Routing: []Route{{To: "team:x", Channels: []string{"pigeon"}}}}, "unknown channel"},
		{Rule{ID: "a", Team: "t", Match: leaf("x", "eq", 1), Presentation: Presentation{Title: "{{x | nope}}"}}, "unknown filter"},
		{Rule{ID: "a", Team: "t", Match: Condition{Field: "x", Op: "eq", All: []Condition{leaf("y", "eq", 1)}}}, "exactly one"},
	}
	for _, tc := range bad {
		_, err := Compile(tc.rule, time.Minute)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("rule %+v: err = %v, want containing %q", tc.rule, err, tc.want)
		}
	}
}

func TestFingerprintDedup(t *testing.T) {
	c := mustCompile(t, Rule{Match: leaf("type", "eq", "WORKFLOW_FAILED")})
	a := c.Fingerprint(doc(t, workflowFailed))
	b := c.Fingerprint(doc(t, strings.Replace(workflowFailed, "WF-20261023-00042", "WF-20261023-00043", 1)))
	if a != b {
		t.Error("same error on a different workflow run should fold into one alert by default")
	}
	other := c.Fingerprint(doc(t, strings.Replace(workflowFailed, `"TIMEOUT"`, `"REJECTED"`, 1)))
	if a == other {
		t.Error("different error codes should be different alerts")
	}

	perWorkflow := mustCompile(t, Rule{Match: leaf("type", "eq", "WORKFLOW_FAILED"), Dedup: Dedup{Key: "{{workflowId}}"}})
	x := perWorkflow.Fingerprint(doc(t, workflowFailed))
	y := perWorkflow.Fingerprint(doc(t, strings.Replace(workflowFailed, "00042", "00043", 1)))
	if x == y {
		t.Error("explicit dedup key should separate workflow runs")
	}
}

func TestRenderDefaults(t *testing.T) {
	c := mustCompile(t, Rule{Match: leaf("alertname", "exists", true), Presentation: Presentation{
		Fields: []string{"labels.region", "Summary=annotations.summary", "missing.field"},
	}})
	r := c.Render(doc(t, metricBreach))
	if r.Title != "HighErrorRate from payments-api" {
		t.Errorf("default title = %q", r.Title)
	}
	if r.Severity != "critical" || r.Color != "red" {
		t.Errorf("severity/color = %s/%s", r.Severity, r.Color)
	}
	if len(r.Fields) != 2 || r.Fields[0].Label != "Region" || r.Fields[1].Label != "Summary" {
		t.Errorf("fields = %+v", r.Fields)
	}
}

func TestParseYAML(t *testing.T) {
	single := []byte(`
id: rates-big-rejections
team: rates
match:
  all:
    - { field: meta.schema, op: glob, value: "trade.rejected.*" }
    - { field: payload.notional, op: gt, value: 10000000 }
routing:
  - { to: team:rates, channels: [email, ui] }
`)
	rs, err := ParseYAML(single)
	if err != nil || len(rs) != 1 {
		t.Fatalf("single: %v %v", rs, err)
	}
	c := mustCompile(t, rs[0])
	if !c.Matches(doc(t, tradeRejected)) {
		t.Error("YAML rule should match the trade event")
	}
	list := []byte("rules:\n  - {id: a, team: x, match: {field: a, op: exists}}\n  - {id: b, team: x, match: {field: b, op: exists}}\n")
	if rs, err := ParseYAML(list); err != nil || len(rs) != 2 {
		t.Fatalf("list: %v %v", rs, err)
	}
}

func BenchmarkMatch(b *testing.B) {
	c, _ := Compile(Rule{ID: "r", Team: "t", Match: Condition{All: []Condition{
		leaf("meta.schema", "glob", "trade.rejected.*"),
		leaf("payload.desk", "eq", "rates"),
		leaf("payload.notional", "gt", 10_000_000),
	}}}, time.Minute)
	env := &event.Envelope{Payload: json.RawMessage(tradeRejected)}
	b.ReportAllocs()
	for b.Loop() {
		if !c.Matches(schema.NewDoc(env)) {
			b.Fatal("no match")
		}
	}
}

func TestHumanizeLabels(t *testing.T) {
	cases := map[string]string{"payload.tradeId": "Trade Id", `Amt.\@Ccy`: "Ccy", "$severity": "Severity", "error_code": "Error code"}
	for in, want := range cases {
		if got := humanize(in); got != want {
			t.Errorf("humanize(%q) = %q, want %q", in, got, want)
		}
	}
}
