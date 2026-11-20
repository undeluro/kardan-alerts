package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/privacy"
	"github.com/undeluro/kardan-alerts/internal/tmpl"
)

// alertReport exports an incident report in Markdown: summary, timeline of
// what happened and who did what, runbook steps with results, and the audit
// entries with their hashes and the chain's verification at export time.
func (s *Server) alertReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, err := s.Store.Get(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	notes, err := s.Store.Notifications(ctx, a.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	runs, err := alerting.RunsForAlert(ctx, s.Store.Pool, a.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	entries, err := s.timeline(r, a.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	verify, err := s.Audit.Verify(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}

	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	utc := func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") }
	cell := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ") }

	p("# Incident report: %s\n\n", a.Title)
	p("Generated %s by Kardan · alert `%s` · status **%s**\n\n", utc(time.Now()), a.ID, a.Status)
	p("## Summary\n\n| | |\n|---|---|\n")
	p("| Severity | %s |\n| Team | %s |\n| Rule | `%s` |\n| Source | %s |\n", a.Severity, a.Team, a.RuleID, a.SourceID)
	p("| First seen | %s |\n| Last seen | %s |\n| Occurrences | %d |\n", utc(a.FirstSeen), utc(a.LastSeen), a.Count)
	if a.NotionalUSD != nil {
		p("| Money at risk | USD %s |\n", tmpl.FormatNumber(*a.NotionalUSD))
	}
	p("| Impact (0-100) | %d |\n", a.Impact)
	if a.CutoffName != nil && a.CutoffAt != nil {
		p("| Cut-off | %s at %s |\n", *a.CutoffName, utc(*a.CutoffAt))
	}
	if a.IncidentID != nil {
		p("| Incident | `%s` |\n", *a.IncidentID)
	}
	if a.AckedBy != nil && a.AckedAt != nil {
		p("| Acknowledged | %s, %s |\n", cell(*a.AckedBy), utc(*a.AckedAt))
	}
	if a.ResolvedBy != nil && a.ResolvedAt != nil {
		p("| Resolved | %s, %s |\n", cell(*a.ResolvedBy), utc(*a.ResolvedAt))
		p("| Time to resolve | %s |\n", a.ResolvedAt.Sub(a.FirstSeen).Round(time.Second))
	}
	if a.Body != "" {
		p("\n%s\n", a.Body)
	}
	if len(a.Fields) > 0 {
		p("\n### Details\n\n")
		for _, f := range a.Fields {
			v := f.Value
			if f.Sensitive {
				v = privacy.Mask(v) + " (masked)"
			}
			p("- **%s:** %s\n", f.Label, v)
		}
	}

	// Timeline: the event, every message, every recorded action.
	type item struct {
		at   time.Time
		text string
	}
	items := []item{{a.FirstSeen, "First event received from " + a.SourceID}}
	for _, n := range notes {
		if n.Status == "standby" || n.Status == "cancelled" {
			continue
		}
		at := n.CreatedAt
		if n.SentAt != nil {
			at = *n.SentAt
		}
		what := fmt.Sprintf("%s to %s (%s): %s", strings.ToUpper(n.Channel), n.Recipient, n.Kind, n.Status)
		if n.Reason != "" {
			what += " — " + n.Reason
		}
		items = append(items, item{at, what})
	}
	for _, e := range entries {
		items = append(items, item{e.At, fmt.Sprintf("**%s** by %s%s (audit #%d)", e.Action, e.Actor, detailSummary(e.Detail), e.Seq)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	p("\n## Timeline\n\n")
	for _, it := range items {
		p("- `%s` %s\n", it.at.UTC().Format("15:04:05"), it.text)
	}

	if len(runs) > 0 {
		p("\n## Runbook steps\n\n| Runbook | Step | What | Status | Requested by | Approved by | Result |\n|---|---|---|---|---|---|---|\n")
		for _, run := range runs {
			approved := ""
			if run.ApprovedBy != nil {
				approved = *run.ApprovedBy
			}
			if run.DecidedBy != nil && run.Status == alerting.RunRejected {
				approved = "rejected by " + *run.DecidedBy
			}
			result := run.Output
			if result == "" {
				result = run.Reason
			}
			p("| %s | %d | %s | %s | %s | %s | %s |\n", run.RunbookID, run.Step+1, cell(run.Label), run.Status,
				cell(run.RequestedBy), cell(approved), cell(result))
		}
	}

	p("\n## Audit trail\n\n")
	if len(entries) == 0 {
		p("No audited actions on this alert.\n")
	} else {
		p("| # | Time | Actor | Action | Hash |\n|---|---|---|---|---|\n")
		for _, e := range entries {
			p("| %d | %s | %s | %s | `%s…` |\n", e.Seq, utc(e.At), cell(e.Actor), e.Action, e.Hash[:16])
		}
	}
	if verify.OK {
		p("\nChain verified at export: **intact**, %d entries, head `%s`.\n", verify.Entries, verify.Head)
	} else {
		p("\nChain verified at export: **BROKEN at entry %d** (%s).\n", verify.BrokenAt, verify.Reason)
	}
	// The raw event is left out on purpose: reports travel as files, and the
	// payload carries unmasked account numbers and names. It stays in Kardan.
	p("\nFirst event `%s` (%s, from %s) is kept in Kardan; it is not copied here because it holds unmasked data.\n",
		a.FirstEventID, a.SampleFormat, a.SourceID)
	name := fmt.Sprintf("kardan-incident-%s.md", alerting.ShortCode(a.ID))
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write([]byte(b.String()))
}

// detailSummary picks the readable parts of an audit entry's detail.
func detailSummary(raw json.RawMessage) string {
	var d map[string]any
	if json.Unmarshal(raw, &d) != nil || len(d) == 0 {
		return ""
	}
	var parts []string
	for _, k := range []string{"label", "via", "reason", "output"} {
		if v, ok := d[k].(string); ok && v != "" {
			if len(v) > 120 {
				v = v[:120] + "…"
			}
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, " · ")
}
