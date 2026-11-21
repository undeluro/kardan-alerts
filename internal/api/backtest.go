package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/finance"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

const (
	backtestMaxEvents = 200_000
	backtestBudget    = 8 * time.Second
	backtestBuckets   = 24
)

type backtestSample struct {
	ID         string    `json:"id"`
	ReceivedAt time.Time `json:"receivedAt"`
	SourceID   string    `json:"sourceId"`
	Title      string    `json:"title"`
	Severity   string    `json:"severity"`
}

type backtestBucket struct {
	At      time.Time `json:"at"`
	Matched int       `json:"matched"`
}

// backtestRule replays recent events from the events topic through a draft
// rule without creating alerts or sending anything:
// POST /api/rules/backtest {"ruleYaml": "...", "hours": 1}
func (s *Server) backtestRule(w http.ResponseWriter, r *http.Request) {
	if s.Replay == nil {
		writeErr(w, http.StatusServiceUnavailable, "event replay is not configured on this replica")
		return
	}
	var req struct {
		RuleYAML string      `json:"ruleYaml"`
		Rule     *rules.Rule `json:"rule"`
		Hours    float64     `json:"hours"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var rule rules.Rule
	switch {
	case req.RuleYAML != "":
		rs, err := rules.ParseYAML([]byte(req.RuleYAML))
		if err != nil || len(rs) != 1 {
			msg := "expected exactly one rule"
			if err != nil {
				msg = "YAML: " + err.Error()
			}
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": msg})
			return
		}
		rule = rs[0]
	case req.Rule != nil:
		rule = *req.Rule
		rule.Match.Normalize()
	default:
		writeErr(w, http.StatusBadRequest, "rule or ruleYaml is required")
		return
	}
	if rule.ID == "" {
		rule.ID = "draft"
	}
	c, err := rules.Compile(rule, s.DefaultWindow)
	if err == nil && s.CheckRule != nil {
		err = s.CheckRule(c)
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	hours := req.Hours
	if hours <= 0 {
		hours = 1
	}
	if hours > 24 {
		hours = 24
	}
	writeJSON(w, http.StatusOK, s.runBacktest(r.Context(), c, time.Duration(hours*float64(time.Hour))))
}

type matchedEvent struct {
	at    time.Time
	fp    string
	value float64
}

func (s *Server) runBacktest(ctx context.Context, c *rules.Compiled, span time.Duration) map[string]any {
	ctx, cancel := context.WithTimeout(ctx, backtestBudget)
	defer cancel()
	now := time.Now().UTC()
	since := now.Add(-span)
	step := span / backtestBuckets
	buckets := make([]backtestBucket, backtestBuckets)
	for i := range buckets {
		buckets[i].At = since.Add(time.Duration(i) * step)
	}
	fx := s.fxTable()
	matched, recoveries := 0, 0
	fingerprints := map[string]bool{}
	bySource := map[string]int{}
	bySeverity := map[string]int{}
	var samples []backtestSample
	var hits []matchedEvent

	st, err := s.Replay.Since(ctx, since, backtestMaxEvents, func(env *event.Envelope) {
		doc := schema.NewDoc(env)
		doc.FX = fx
		if s.Schemas != nil {
			_, doc.Overrides, _ = s.Schemas.Identify(env.Payload)
		}
		if c.IsRecovery(doc) {
			recoveries++
			return
		}
		if !c.Matches(doc) {
			return
		}
		matched++
		bySource[env.SourceID]++
		if i := int(env.ReceivedAt.Sub(since) / step); i >= 0 && i < backtestBuckets {
			buckets[i].Matched++
		}
		rendered := c.Render(doc)
		bySeverity[rendered.Severity]++
		fp := c.Fingerprint(doc)
		v := 1.0
		if c.Kind() == rules.KindAggregate {
			_, fp = c.AggregateKey(doc)
			if f := c.Aggregate().SumField; f != "" {
				v = 0
				if x := doc.Get(f); x.Exists() {
					v = x.Float()
				}
			}
		} else if c.Grouped() {
			ws, _ := c.WindowFor(env.ReceivedAt)
			fp += "@" + ws.Format(time.RFC3339)
		}
		fingerprints[fp] = true
		hits = append(hits, matchedEvent{at: env.ReceivedAt, fp: fp, value: v})
		title := rendered.Title
		if c.Kind() != rules.KindEvent {
			// Window and deadline values only exist when the rule fires.
			title = env.SourceID + " event"
			if v := doc.Get("$type"); v.Exists() {
				title = v.String()
			}
		}
		samples = append(samples, backtestSample{ID: env.ID, ReceivedAt: env.ReceivedAt, SourceID: env.SourceID,
			Title: title, Severity: rendered.Severity})
	})
	if err != nil {
		return map[string]any{"valid": true, "error": "replay failed: " + err.Error()}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].ReceivedAt.After(samples[j].ReceivedAt) })
	if len(samples) > 5 {
		samples = samples[:5]
	}
	alerts := len(fingerprints)
	note := ""
	switch c.Kind() {
	case rules.KindAggregate:
		alerts = simulateAggregate(hits, c.Aggregate())
		note = "Matched counts events that pass the filter; alerts counts how often a window total would have crossed the threshold."
	case rules.KindAbsence:
		alerts = 0
		note = "Absence rules fire when these events stop arriving; matched shows how many heartbeats or files were seen."
	}
	return map[string]any{
		"valid": true, "kind": c.Kind(), "scanned": st.Scanned, "matched": matched, "alerts": alerts,
		"recoveries": recoveries, "from": st.From, "to": st.To, "truncated": st.Truncated,
		"buckets": buckets, "samples": samples, "bySource": bySource, "bySeverity": bySeverity, "note": note,
	}
}

// simulateAggregate counts threshold crossings of a sliding window per key,
// the way the processor opens aggregate alerts (one per crossing while open).
func simulateAggregate(hits []matchedEvent, spec rules.AggregateSpec) int {
	sort.Slice(hits, func(i, j int) bool { return hits[i].at.Before(hits[j].at) })
	type win struct {
		items  []matchedEvent
		total  float64
		firing bool
	}
	keys := map[string]*win{}
	fires := 0
	for _, h := range hits {
		w := keys[h.fp]
		if w == nil {
			w = &win{}
			keys[h.fp] = w
		}
		w.items = append(w.items, h)
		w.total += h.value
		for len(w.items) > 0 && h.at.Sub(w.items[0].at) > spec.Window {
			w.total -= w.items[0].value
			w.items = w.items[1:]
		}
		if w.total >= spec.Threshold {
			if !w.firing {
				fires++
				w.firing = true
			}
		} else {
			w.firing = false
		}
	}
	return fires
}

func (s *Server) fxTable() *finance.FX {
	if s.FX == nil {
		return nil
	}
	return s.FX()
}
