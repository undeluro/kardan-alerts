package runbooks

import (
	"path/filepath"
	"runtime"
	"testing"
)

// The seeded runbooks in config/runbooks must all load, in their formats.
func TestSeededRunbooks(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	list, errs := LoadDir(filepath.Join(filepath.Dir(file), "..", "..", "config", "runbooks"))
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	formats := map[string]string{}
	for _, rb := range list {
		formats[rb.ID] = rb.Format
		for i, s := range rb.Steps {
			t.Logf("%s %d %s %s %v %q if=%q", rb.ID, i+1, s.Kind, s.Action, s.Params, s.Approval, s.If)
		}
	}
	want := map[string]string{"settlement-timeout": FormatYAML, "payments-high-error-rate": FormatMarkdown,
		"pricing-latency": FormatText, "payment-rejection": FormatYAML, "limit-breach": FormatYAML,
		"stale-market-data": FormatChecklist, "eod-positions-missing": FormatLink}
	for id, f := range want {
		if formats[id] != f {
			t.Errorf("%s: format %q, want %q", id, formats[id], f)
		}
	}
}
