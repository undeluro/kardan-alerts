package schema

import (
	"encoding/json"
	"testing"

	"github.com/undeluro/kardan-alerts/internal/event"
)

func TestFacetsAcrossSchemas(t *testing.T) {
	cases := []struct {
		payload                string
		severity, service, typ string
	}{
		{`{"type":"WORKFLOW_FAILED","severity":"ERROR","environment":"prod"}`, "error", "", "WORKFLOW_FAILED"},
		{`{"alertname":"HighErrorRate","labels":{"service":"payments-api","severity":"critical"}}`, "critical", "payments-api", "HighErrorRate"},
		{`{"meta":{"schema":"trade.rejected.v2","producer":"trade-gateway"},"payload":{}}`, "", "trade-gateway", "trade.rejected.v2"},
		{`{"level":"WARN","app":"pricing-engine-3"}`, "warning", "pricing-engine-3", ""},
		{`{"priority":"P1","type":{"nested":"object"}}`, "critical", "", ""},
	}
	for _, tc := range cases {
		d := NewDoc(&event.Envelope{SourceID: "src", Payload: json.RawMessage(tc.payload)})
		if got := d.Get("$severity").String(); got != tc.severity {
			t.Errorf("%s: $severity = %q, want %q", tc.payload, got, tc.severity)
		}
		if got := d.Get("$service").String(); got != tc.service {
			t.Errorf("%s: $service = %q, want %q", tc.payload, got, tc.service)
		}
		if got := d.Get("$type").String(); got != tc.typ {
			t.Errorf("%s: $type = %q, want %q", tc.payload, got, tc.typ)
		}
		if got := d.Get("$source").String(); got != "src" {
			t.Errorf("$source = %q", got)
		}
	}
}

func TestParseLevel(t *testing.T) {
	if ParseLevel("Sev2") != LevelError || ParseLevel(" fatal ") != LevelCritical || ParseLevel("???") != LevelUnknown {
		t.Error("severity aliases broken")
	}
	if !(LevelCritical > LevelError && LevelError > LevelWarning && LevelWarning > LevelInfo) {
		t.Error("levels must be ordered")
	}
}
