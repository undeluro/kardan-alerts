package rules

import (
	"strings"
	"testing"
	"time"
)

func TestAggregateRule(t *testing.T) {
	c := mustCompile(t, Rule{
		Kind:      KindAggregate,
		Match:     leaf("meta.schema", "glob", "trade.rejected.*"),
		Aggregate: &Aggregate{By: []string{"payload.desk"}, Window: "1h", Sum: "payload.notional", Threshold: 50_000_000},
	})
	if c.Kind() != KindAggregate || c.Aggregate().Window != time.Hour || c.Aggregate().SumField != "payload.notional" {
		t.Errorf("spec = %+v", c.Aggregate())
	}
	v1, fp1 := c.AggregateKey(doc(t, tradeRejected))
	_, fp2 := c.AggregateKey(doc(t, strings.Replace(tradeRejected, "T-998877", "T-1", 1)))
	_, fp3 := c.AggregateKey(doc(t, strings.Replace(tradeRejected, `"rates"`, `"fx"`, 1)))
	if v1[0] != "rates" || fp1 != fp2 || fp1 == fp3 {
		t.Errorf("aggregate keys: %v %s %s %s", v1, fp1, fp2, fp3)
	}
	rs := Build([]Rule{c.Rule, {ID: "plain", Team: "t", Match: leaf("meta.schema", "exists", true)}}, time.Minute)
	if len(rs.Match(doc(t, tradeRejected))) != 1 || len(rs.Aggregates(doc(t, tradeRejected))) != 1 {
		t.Error("Match must return event rules only; Aggregates the aggregate ones")
	}
}

func TestAbsenceRule(t *testing.T) {
	c := mustCompile(t, Rule{
		Kind:    KindAbsence,
		Match:   leaf("type", "eq", "EOD_POSITIONS"),
		Absence: &Absence{By: "custodian", Expect: []string{"BNYM", "STATESTREET"}, Deadline: "18:30", Calendar: "XLON"},
	})
	spec := c.Absence()
	if spec.Deadline != 18*60+30 || spec.Calendar != "XLON" || len(spec.Expect) != 2 {
		t.Errorf("spec = %+v", spec)
	}
	if c.AbsenceFingerprint("BNYM", "2026-11-20") == c.AbsenceFingerprint("BNYM", "2026-11-23") {
		t.Error("deadline alerts are per business day")
	}
	hb := mustCompile(t, Rule{Kind: KindAbsence, Match: leaf("type", "eq", "TICK"), Absence: &Absence{By: "symbol", Every: "30s"}})
	if hb.Absence().Deadline != -1 || hb.Absence().Every != 30*time.Second {
		t.Errorf("heartbeat = %+v", hb.Absence())
	}
}

func TestTimeAwareValidation(t *testing.T) {
	base := func(r Rule) Rule {
		r.ID, r.Team = "x", "t"
		if r.Match.Field == "" {
			r.Match = leaf("a", "exists", true)
		}
		return r
	}
	bad := map[string]Rule{
		"kind":       base(Rule{Kind: "sometimes"}),
		"no agg":     base(Rule{Kind: KindAggregate}),
		"agg thresh": base(Rule{Kind: KindAggregate, Aggregate: &Aggregate{Window: "1h"}}),
		"agg window": base(Rule{Kind: KindAggregate, Aggregate: &Aggregate{Window: "forever", Threshold: 1}}),
		"abs both":   base(Rule{Kind: KindAbsence, Absence: &Absence{Deadline: "18:30", Every: "1m", Calendar: "X"}}),
		"abs cal":    base(Rule{Kind: KindAbsence, Absence: &Absence{Deadline: "18:30"}}),
		"abs expect": base(Rule{Kind: KindAbsence, Absence: &Absence{Every: "1m", Expect: []string{"A"}}}),
		"during":     base(Rule{During: &During{Calendar: "XLON", Otherwise: "later"}}),
		"esc order": base(Rule{Escalation: []EscalationStep{
			{After: "10m", To: "team:t", Channels: []string{"sms"}}, {After: "5m", To: "user:u", Channels: []string{"phone"}}}}),
		"esc chan":     base(Rule{Escalation: []EscalationStep{{After: "10m", To: "team:t", Channels: []string{"ui"}}}}),
		"agg on event": base(Rule{Aggregate: &Aggregate{Window: "1h", Threshold: 1}}),
	}
	for name, r := range bad {
		if _, err := Compile(r, time.Minute); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	c := mustCompile(t, base(Rule{During: &During{Calendar: "XLON", Otherwise: "info"}, Cutoff: "auto",
		Escalation: []EscalationStep{{After: "10m", To: "team:t", Channels: []string{"sms"}}, {After: "20m", To: "user:u", Channels: []string{"phone"}}}}))
	if cal, off := c.During(); cal != "XLON" || off != "info" {
		t.Errorf("during = %s/%s", cal, off)
	}
	if len(c.EscalationSteps()) != 2 || c.CutoffMode() != "auto" || len(c.Calendars()) != 1 {
		t.Error("escalation / cutoff / calendars")
	}
	// Unknown calendars are reported by the Build check, not Compile.
	rs := Build([]Rule{c.Rule}, time.Minute, func(c *Compiled) error {
		for _, id := range c.Calendars() {
			if id != "KNOWN" {
				return errUnknownCal(id)
			}
		}
		return nil
	})
	if len(rs.Rules) != 0 || len(rs.Errors) != 1 || !strings.Contains(rs.Errors[0], "XLON") {
		t.Errorf("build check: %v", rs.Errors)
	}
}

type errUnknownCal string

func (e errUnknownCal) Error() string { return "unknown calendar " + string(e) }

func TestFieldFilters(t *testing.T) {
	c := mustCompile(t, Rule{Match: leaf("payload.notional", "exists", true),
		Presentation: Presentation{Fields: []string{"Notional=payload.notional | number", "payload.desk"}}})
	r := c.Render(doc(t, tradeRejected))
	if len(r.Fields) != 2 || r.Fields[0].Label != "Notional" || !strings.Contains(r.Fields[0].Value, ",") || r.Fields[1].Label != "Desk" {
		t.Errorf("fields = %+v", r.Fields)
	}
	if _, err := Compile(Rule{ID: "x", Team: "t", Match: leaf("a", "exists", true),
		Presentation: Presentation{Fields: []string{"X=a | nosuchfilter"}}}, time.Minute); err == nil {
		t.Error("unknown filter must be rejected")
	}
}
