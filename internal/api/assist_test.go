package api

import (
	"testing"
	"time"

	"github.com/undeluro/kardan-alerts/internal/rules"
)

func TestSuggestChangeCompiles(t *testing.T) {
	base := rules.Rule{ID: "r", Team: "t", Match: rules.Condition{Field: "$severity", Op: "severityAtLeast", Value: "error"},
		Routing: []rules.Route{{To: "team:t", Channels: []string{"preferences"}}}, Presentation: rules.Presentation{Title: "x"}}
	for _, sig := range []string{"flapping", "noise", "ignored"} {
		r, _, ok := suggestChange(base, sig)
		if !ok {
			t.Fatalf("%s: no suggestion", sig)
		}
		if _, err := rules.Compile(r, 10*time.Minute); err != nil {
			t.Errorf("%s: %v", sig, err)
		}
	}
	if r, _, _ := suggestChange(base, "noise"); r.Presentation.Severity != "warning" {
		t.Errorf("noise should pin warning, got %q", r.Presentation.Severity)
	}
	info := base
	info.Presentation.Severity = "info"
	if _, _, ok := suggestChange(info, "ignored"); ok {
		t.Error("info cannot be lowered further")
	}
	rec := base
	rec.AutoResolve = &rules.AutoResolve{When: &rules.Condition{Field: "status", Op: "eq", Value: "resolved"}}
	r, note, ok := suggestChange(rec, "flapping")
	if !ok || note == "" || r.Group != nil || r.Presentation.Severity != "warning" {
		t.Errorf("recovery rule: %+v %q %v", r.Presentation, note, ok)
	}
	if _, err := rules.Compile(r, 10*time.Minute); err != nil {
		t.Error(err)
	}
}
