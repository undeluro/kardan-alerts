package ai

import (
	"strings"
	"testing"
	"time"

	"github.com/undeluro/kardan-alerts/internal/rules"
)

func TestRedact(t *testing.T) {
	in := "Call +44 7700 900101 or mail ops@bank.example; IBAN DE89370400440532013000; api_key=sk-abcdefghijklmnopqrstu token: abc123"
	out := Redact(in)
	for _, leak := range []string{"7700", "ops@bank", "DE8937", "sk-abc", "abc123"} {
		if strings.Contains(out, leak) {
			t.Errorf("redacted text still contains %q: %s", leak, out)
		}
	}
}

func compile(t *testing.T, res RuleResult) *rules.Compiled {
	t.Helper()
	c, err := rules.Compile(res.Rule, 10*time.Minute)
	if err != nil {
		t.Fatalf("draft does not compile: %v\n%+v", err, res.Rule)
	}
	return c
}

func TestHeuristicRuleAmounts(t *testing.T) {
	req := RuleRequest{Sentence: "Page the on-call when a payment over USD 50M is rejected for counterparty ACME-CAPITAL", Team: "payments"}
	res := buildRule(HeuristicRule(req), req)
	c := compile(t, res)
	r := c.Rule
	if r.Routing[0].To != "oncall:payments" || !contains(r.Routing[0].Channels, "phone") {
		t.Errorf("routing = %+v", r.Routing)
	}
	got := map[string]any{}
	for _, cond := range r.Match.All {
		got[cond.Field] = cond.Value
	}
	if got["$notionalUsd"] != int64(50e6) {
		t.Errorf("$notionalUsd = %v, want 5e7 (conditions %+v)", got["$notionalUsd"], r.Match.All)
	}
	if got["$counterparty"] != "ACME-CAPITAL" {
		t.Errorf("$counterparty = %v", got["$counterparty"])
	}
}

func TestHeuristicAggregate(t *testing.T) {
	req := RuleRequest{Sentence: "Alert me when 5 rejections happen in 10 minutes for the same counterparty", Team: "rates"}
	res := buildRule(HeuristicRule(req), req)
	c := compile(t, res)
	if c.Kind() != rules.KindAggregate {
		t.Fatalf("kind = %s", c.Kind())
	}
	if a := res.Rule.Aggregate; a.Threshold != 5 || a.Window != "10m" || len(a.By) != 1 || a.By[0] != "$counterparty" {
		t.Errorf("aggregate = %+v", a)
	}
}

func TestBuildRuleFlagsUnknownFields(t *testing.T) {
	d := RuleDraft{Name: "x", Combine: "all", Conditions: []DraftCond{{Field: "payload.madeUp", Op: "gt", Value: "2.5bn"}},
		Groups: []DraftGrp{{Combine: "any", Conditions: []DraftCond{{Field: "$desk", Op: "in", Value: "rates, fx"}, {Field: "$env", Op: "eq", Value: "prod", Negate: true}}}}}
	res := buildRule(d, RuleRequest{Team: "t", Fields: []string{"payload.notional"}})
	c := compile(t, res)
	if len(res.Warnings) != 1 {
		t.Errorf("warnings = %v", res.Warnings)
	}
	if v := res.Rule.Match.All[0].Value; v != int64(2.5e9) {
		t.Errorf("amount = %v", v)
	}
	if g := res.Rule.Match.All[1]; len(g.Any) != 2 || g.Any[1].Not == nil {
		t.Errorf("group = %+v", g)
	}
	_ = c
}

func TestRunbookYAMLKeepsPolicy(t *testing.T) {
	d := runbookDraft{Steps: []draftStep{
		{Kind: "check", Text: "Is the gateway healthy?", Action: "http_get", Params: []draftParam{{Key: "url", Value: "http://gw/health"}}, Approval: "auto"},
		{Kind: "action", Text: "Pause trading for {{$desk}}", Action: "pause_trading", Params: []draftParam{{Key: "desk", Value: "{{$desk}}"}}, Approval: "auto"},
		{Kind: "action", Text: "Reboot the mainframe", Action: "reboot_everything", Approval: "auto"},
	}}
	res := runbookYAML(d, RunbookRequest{ID: "rb", Actions: []ActionInfo{{Type: "http_get", Risk: "low"}, {Type: "pause_trading", Risk: "high"}}})
	if !strings.Contains(res.YAML, "approval: required") {
		t.Errorf("high-risk action must require approval:\n%s", res.YAML)
	}
	if strings.Contains(res.YAML, "reboot_everything") || len(res.Warnings) != 1 {
		t.Errorf("unknown action must become text: %v\n%s", res.Warnings, res.YAML)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestHeuristicCurrencyAndWindow(t *testing.T) {
	req := RuleRequest{Sentence: "Email me when a payment over EUR 2.5M is rejected", Team: "p", Fields: []string{"tags.10", "payment.amount"}}
	d := HeuristicRule(req)
	got := map[string]string{}
	for _, c := range d.Conditions {
		got[c.Field] = c.Value
	}
	if got["$notional"] != "2500000" || got["$currency"] != "EUR" || got["$notionalUsd"] != "" {
		t.Errorf("conditions = %+v", d.Conditions)
	}
	req = RuleRequest{Sentence: "Page the on-call when 5 trades for the same counterparty are rejected within 10 minutes", Team: "r",
		Fields: []string{"tags.10"}}
	d = HeuristicRule(req)
	if d.Kind != "aggregate" || d.Aggregate.Window != "10m" || d.Aggregate.Threshold != 5 || d.Route != "oncall" {
		t.Errorf("draft = %+v", d)
	}
	for _, c := range d.Conditions {
		if c.Field == "tags.10" {
			t.Errorf("numeric field matched a number word: %+v", c)
		}
	}
}

func TestHeuristicServiceAndCode(t *testing.T) {
	req := RuleRequest{Sentence: "Text me when the settlements service reports a TIMEOUT error", Team: "s", Fields: []string{"labels.service"}}
	d := HeuristicRule(req)
	got := map[string]string{}
	for _, c := range d.Conditions {
		got[c.Field] = c.Value
	}
	if got["$service"] != "settlements" || got["$type"] != "(?i)TIMEOUT" || got["labels.service"] != "" {
		t.Errorf("conditions = %+v", d.Conditions)
	}
	if len(d.Channels) == 0 || d.Channels[0] != "sms" {
		t.Errorf("channels = %v", d.Channels)
	}
	res := buildRule(d, req)
	compile(t, res)
}
