package runbooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

// Appendix B, verbatim.
const appendixYAML = `runbook: settlement-timeout
matches:
  type: WORKFLOW_FAILED
  "error.code": TIMEOUT
steps:
  - check: "Is counterparty-service healthy?"
    action: http_get
    url: "http://counterparty-service/health"
  - action: retry_workflow
    workflowId: "{{workflowId}}"
    approval: none
  - if: "retry fails twice"
    action: escalate
    team: counterparty-support
    approval: required
`

const appendixMarkdown = `# HighErrorRate on payments-api
1. Open the payments dashboard and check if errors are in one region only.
2. If one region: fail traffic over to the other region (needs approval from the team lead).
3. If all regions: check the last deployment and roll it back.
4. Post an update in the #payments-incidents channel every 15 minutes.
`

const appendixText = `When pricing latency goes over a second, it's usually the cache. Restart the pricing-engine instance named in the alert, wait 2 minutes, and if latency is still high, call the pricing on-call person.`

func mustParse(t *testing.T, src string, meta Meta) *Runbook {
	t.Helper()
	rb, err := Parse(src, meta)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return rb
}

func docOf(t *testing.T, payload string) *schema.Doc {
	t.Helper()
	return schema.NewDoc(&event.Envelope{ID: "e1", Payload: json.RawMessage(payload)})
}

func TestAppendixYAML(t *testing.T) {
	rb := mustParse(t, appendixYAML, Meta{})
	if rb.ID != "settlement-timeout" || rb.Format != FormatYAML || len(rb.Steps) != 3 {
		t.Fatalf("runbook = %+v", rb)
	}
	s := rb.Steps
	if s[0].Kind != KindCheck || s[0].Action != "http_get" || s[0].Params["url"] != "http://counterparty-service/health" {
		t.Errorf("step 1 = %+v", s[0])
	}
	if s[1].Kind != KindAction || s[1].Action != "retry_workflow" || s[1].Approval != "none" {
		t.Errorf("step 2 = %+v", s[1])
	}
	if s[2].Action != "escalate" || s[2].If != "retry fails twice" || s[2].Params["team"] != "counterparty-support" || s[2].Approval != "required" {
		t.Errorf("step 3 = %+v", s[2])
	}
	if Attempts(s[2].If) != 2 || !OnFailure(s[2].If) {
		t.Error("'retry fails twice' means two attempts, then the step runs")
	}
	wf := docOf(t, `{"type":"WORKFLOW_FAILED","workflowId":"WF-20261023-00042","error":{"code":"TIMEOUT"}}`)
	if !rb.MatchesDoc(wf) || rb.MatchesDoc(docOf(t, `{"type":"WORKFLOW_FAILED","error":{"code":"DENIED"}}`)) {
		t.Error("matches: type and error.code")
	}
	filled := rb.Fill(wf.Get)
	if filled[1].Params["workflowId"] != "WF-20261023-00042" || len(filled[1].Missing) != 0 {
		t.Errorf("filled = %+v", filled[1])
	}
}

func TestAppendixMarkdown(t *testing.T) {
	rb := mustParse(t, appendixMarkdown, Meta{Team: "payments"})
	if rb.Name != "HighErrorRate on payments-api" || rb.ID != "higherrorrate-on-payments-api" || rb.Format != FormatMarkdown {
		t.Fatalf("runbook = %s %s %s", rb.Name, rb.ID, rb.Format)
	}
	var got []string
	for _, s := range rb.Steps {
		got = append(got, s.Kind+":"+s.Action+":"+s.Approval+":"+s.If)
	}
	want := []string{
		"check:::", // open the dashboard and check (manual, one step)
		"action:failover_region:required:one region", // needs approval from the team lead
		"check:::all regions",                        // check the last deployment
		"action:rollback_deployment::all regions",    // and roll it back
		"action:notify_channel::",                    // post an update in #payments-incidents
	}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("steps:\n got %v\nwant %v", got, want)
	}
	last := rb.Steps[len(rb.Steps)-1]
	if last.Params["channel"] != "#payments-incidents" || last.Every != "15m0s" {
		t.Errorf("notify step = %+v", last)
	}
}

func TestAppendixText(t *testing.T) {
	rb := mustParse(t, appendixText, Meta{ID: "pricing-latency"})
	if rb.Format != FormatText || !strings.Contains(rb.Summary, "usually the cache") {
		t.Fatalf("format %s summary %q", rb.Format, rb.Summary)
	}
	if len(rb.Steps) != 3 {
		t.Fatalf("steps = %+v", rb.Steps)
	}
	if rb.Steps[0].Action != "restart_service" || rb.Steps[0].Params["service"] != "{{$service}}" {
		t.Errorf("restart = %+v", rb.Steps[0])
	}
	if rb.Steps[1].Action != "wait" || rb.Steps[1].Params["duration"] != "2m0s" {
		t.Errorf("wait = %+v", rb.Steps[1])
	}
	if rb.Steps[2].Action != "escalate" || rb.Steps[2].Params["to"] != "oncall:pricing" || rb.Steps[2].If != "latency is still high" {
		t.Errorf("call = %+v", rb.Steps[2])
	}
	pricing := docOf(t, `{"level":"WARN","app":"pricing-engine-3","instrument":"EURUSD","latencyMs":"1240"}`)
	if f := rb.Fill(pricing.Get); f[0].Params["service"] != "pricing-engine-3" {
		t.Errorf("service from the alert = %+v", f[0])
	}
}

func TestChecklistAndLink(t *testing.T) {
	rb := mustParse(t, "---\nrunbook: stale-quotes\nmatches: { type: PRICE_TICK }\n---\n- [ ] Check the vendor status page\n- [ ] Fail over {{instrument}} to the secondary vendor feed\n- [x] Tell the desk quotes may be stale\n", Meta{})
	if rb.Format != FormatChecklist || len(rb.Steps) != 3 || rb.Steps[0].Kind != KindCheck || rb.Steps[1].Action != "failover_feed" {
		t.Errorf("checklist = %s %+v", rb.Format, rb.Steps)
	}
	if rb.Matches == nil {
		t.Error("front matter matches")
	}
	link := mustParse(t, "EOD positions missing: https://wiki.example.com/settlements/eod-positions", Meta{ID: "eod"})
	if link.Format != FormatLink || link.URL != "https://wiki.example.com/settlements/eod-positions" || link.Name != "EOD positions missing" {
		t.Errorf("link = %+v", link)
	}
}

func TestInferVariety(t *testing.T) {
	cases := map[string]string{
		"Escalate to the treasury team":                         "escalate",
		"Open a JIRA ticket for the payments team":              "open_ticket",
		"Repair the creditor details and resubmit the payment":  "resubmit_payment",
		"Pause trading on the affected desk":                    "pause_trading",
		"Notify the risk team":                                  "notify_risk",
		"Fail the EURUSD feed over to the backup vendor":        "failover_feed",
		"Retry the workflow from the failed step":               "retry_workflow",
		"Look at the Grafana board":                             "",
		"Fail {{instrument}} over to the secondary vendor feed": "failover_feed",
	}
	for text, want := range cases {
		if got := Infer(text).Action; got != want {
			t.Errorf("%q -> %q, want %q", text, got, want)
		}
	}
	if Infer("Resubmit the payment (four-eyes)").Approval != "required" {
		t.Error("four-eyes means approval")
	}
}

func TestYAMLErrors(t *testing.T) {
	for name, src := range map[string]string{
		"no steps":     "runbook: x\nsteps: []\n",
		"bad approval": "runbook: x\nsteps:\n  - action: wait\n    approval: maybe\n",
		"empty step":   "runbook: x\nsteps:\n  - approval: none\n",
	} {
		if _, err := Parse(src, Meta{}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

var _ = gjson.Result{}

func TestToYAMLRoundTrip(t *testing.T) {
	rb := mustParse(t, appendixMarkdown, Meta{Team: "payments"})
	y := ToYAML(rb)
	back, err := Parse(y, Meta{})
	if err != nil {
		t.Fatalf("converted YAML does not parse: %v\n%s", err, y)
	}
	if len(back.Steps) != len(rb.Steps) || back.Steps[1].Action != "failover_region" || back.Steps[1].Approval != "required" ||
		back.Steps[4].Every != "15m0s" || back.Team != "payments" {
		t.Errorf("round trip lost data:\n%s", y)
	}
}
