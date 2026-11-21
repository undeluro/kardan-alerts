package proposals

import "testing"

func TestSignals(t *testing.T) {
	stats := []RuleStats{
		{RuleID: "flappy", Alerts: 10, SelfResolved: 7},
		{RuleID: "noisy", Alerts: 8, QuickHuman: 6, PhoneOrSMSSends: 12},
		{RuleID: "quiet-phone", Alerts: 8, QuickHuman: 6}, // nobody paged: no need to lower
		{RuleID: "ignored", Alerts: 6, NeverAcked: 4},
		{RuleID: "rare", Alerts: 2, SelfResolved: 2}, // too few to judge
		{RuleID: "healthy", Alerts: 20, SelfResolved: 2, QuickHuman: 3},
	}
	got := map[string]string{}
	for _, s := range Signals(stats, 5) {
		got[s.Rule] = s.Name
	}
	want := map[string]string{"flappy": "flapping", "noisy": "noise", "ignored": "ignored"}
	if len(got) != len(want) {
		t.Fatalf("signals = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
}
