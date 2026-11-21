package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/undeluro/kardan-alerts/internal/ai"
	"github.com/undeluro/kardan-alerts/internal/audit"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/proposals"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/runbooks"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

func (s *Server) registerAssist(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/status", s.aiStatus)
	mux.HandleFunc("POST /api/ai/rule-draft", s.aiRuleDraft)
	mux.HandleFunc("POST /api/ai/runbook", s.aiRunbook)
	mux.HandleFunc("GET /api/proposals", s.listProposals)
	mux.HandleFunc("POST /api/proposals", s.createProposal)
	mux.HandleFunc("GET /api/proposals/{id}", s.getProposal)
	mux.HandleFunc("POST /api/proposals/{id}/accept", s.decideProposal(true))
	mux.HandleFunc("POST /api/proposals/{id}/reject", s.decideProposal(false))
	mux.HandleFunc("POST /api/proposals/analyze", s.analyzeFatigue)
}

// teamOptedIn reports whether a team allows Claude assistance.
func (s *Server) teamOptedIn(team string) bool {
	t, ok := s.Directory().Team(team)
	return ok && t.AIAssist
}

func (s *Server) aiStatus(w http.ResponseWriter, _ *http.Request) {
	var teams []string
	for _, t := range s.Directory().Teams {
		if t.AIAssist {
			teams = append(teams, t.ID)
		}
	}
	if teams == nil {
		teams = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": s.AI.Enabled(), "model": ai.Model, "teams": teams})
}

// knownFields lists field names (never values) from recent schemas plus facets.
func (s *Server) knownFields(r *http.Request) []string {
	set := map[string]bool{}
	for _, f := range schema.FacetNames() {
		switch f {
		case "$id", "$raw", "$receivedAt":
		default:
			set[f] = true
		}
	}
	if s.Schemas != nil {
		if list, err := s.Schemas.List(r.Context(), 100); err == nil {
			for _, sc := range list {
				for _, f := range sc.Fields {
					set[f.Path] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// aiRuleDraft: POST /api/ai/rule-draft {"sentence": "...", "team": "payments", "sample": "optional event"}
func (s *Server) aiRuleDraft(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sentence string `json:"sentence"`
		Team     string `json:"team"`
		Sample   string `json:"sample"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Sentence = strings.TrimSpace(req.Sentence)
	if req.Sentence == "" || len(req.Sentence) > 2000 {
		writeErr(w, http.StatusBadRequest, "sentence is required (up to 2000 characters)")
		return
	}
	var teams []string
	for _, t := range s.Directory().Teams {
		teams = append(teams, t.ID)
	}
	res := s.AI.DraftRule(r.Context(), ai.RuleRequest{Sentence: req.Sentence, Team: req.Team, Teams: teams,
		Fields: s.knownFields(r)}, s.teamOptedIn(req.Team))
	out := map[string]any{"source": res.Source, "explanation": res.Explanation, "warnings": orEmpty(res.Warnings),
		"fallback": res.Fallback}
	y, err := rules.ToYAML(res.Rule)
	if err != nil {
		s.fail(w, err)
		return
	}
	out["yaml"] = "# Drafted from: " + oneLine(req.Sentence) + "\n" + string(y)
	if err := s.validateRule(res.Rule); err != nil {
		out["valid"], out["error"] = false, err.Error()
	} else {
		out["valid"] = true
	}
	if req.Sample != "" {
		if c, err := rules.Compile(res.Rule, s.DefaultWindow); err == nil {
			env := envelopeFromRaw(s, "draft", []byte(req.Sample))
			doc := schema.NewDoc(env)
			doc.FX = s.fxTable()
			if s.Schemas != nil {
				_, doc.Overrides, _ = s.Schemas.Identify(env.Payload)
			}
			out["matchesSample"] = c.Matches(doc)
		}
	}
	s.record(r, audit.Event{Actor: actor(r), Action: "ai.rule_draft", Subject: "team:" + req.Team,
		Detail: map[string]any{"source": res.Source, "chars": len(req.Sentence)}})
	writeJSON(w, http.StatusOK, out)
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 100 {
		s = s[:100] + "…"
	}
	return s
}

func orEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// aiRunbook structures pasted text: POST /api/ai/runbook {"text": "...", "team": "...", "id": "...", "name": "..."}
// The answer has the same shape as /api/runbooks/parse, plus where it came from.
func (s *Server) aiRunbook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
		Team string `json:"team"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, "text is required")
		return
	}
	var acts []ai.ActionInfo
	if s.Actions != nil {
		for _, d := range s.Actions().List() {
			acts = append(acts, ai.ActionInfo{Type: d.Type, Description: d.Description, Risk: d.Risk, Params: d.Params})
		}
	}
	res, ok := s.AI.StructureRunbook(r.Context(), ai.RunbookRequest{Text: req.Text, ID: req.ID, Name: req.Name, Team: req.Team,
		Actions: acts, Fields: s.knownFields(r)}, s.teamOptedIn(req.Team))
	source := req.Text
	if ok {
		source = res.YAML
	}
	meta := runbooks.Meta{ID: req.ID, Name: req.Name, Team: req.Team}
	if meta.ID == "" {
		meta.ID = "draft"
	}
	rb, err := runbooks.Parse(source, meta)
	out := map[string]any{"source": res.Source, "notes": res.Notes, "warnings": orEmpty(res.Warnings), "fallback": res.Fallback}
	if err != nil {
		out["valid"], out["error"] = false, err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["valid"], out["runbook"], out["yaml"], out["checks"] = true, rb, runbooks.ToYAML(rb), s.stepChecks(rb)
	s.record(r, audit.Event{Actor: actor(r), Action: "ai.runbook_structure", Subject: "team:" + req.Team,
		Detail: map[string]any{"source": res.Source, "steps": len(rb.Steps)}})
	writeJSON(w, http.StatusOK, out)
}

// ---- proposals -------------------------------------------------------------------

func (s *Server) listProposals(w http.ResponseWriter, r *http.Request) {
	list, err := s.Proposals.List(r.Context(), r.URL.Query().Get("status"), 200)
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []*proposals.Proposal{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getProposal(w http.ResponseWriter, r *http.Request) {
	p, err := s.Proposals.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, proposals.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposal": p, "current": s.currentSource(p)})
}

// currentSource is what the proposal would replace, for the diff view.
func (s *Server) currentSource(p *proposals.Proposal) string {
	switch p.Kind {
	case "rule":
		if c := s.Engine.Current().ByID(p.Target); c != nil {
			if y, err := rules.ToYAML(c.Rule); err == nil {
				return string(y)
			}
		}
	case "runbook":
		if rb, ok := s.Runbooks.Current().Get(p.Target); ok {
			return runbooks.ToYAML(rb)
		}
	}
	return ""
}

// createProposal: POST /api/proposals {"kind": "rule", "target": "...", "title": "...", "rationale": "...", "body": "...", "alertId": "..."}
func (s *Server) createProposal(w http.ResponseWriter, r *http.Request) {
	var p proposals.Proposal
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p.Title, p.Target = strings.TrimSpace(p.Title), strings.TrimSpace(p.Target)
	if p.Title == "" || p.Target == "" || strings.TrimSpace(p.Body) == "" {
		writeErr(w, http.StatusBadRequest, "title, target and body are required")
		return
	}
	if p.Source != "ai" {
		p.Source = "person"
	}
	p.Signal, p.ID, p.CreatedBy = "", event.NewID(), actor(r)
	p.CreatedByID = ""
	if user := sessionUser(r); user != nil {
		p.CreatedByID = user.ID
	}
	if err := s.checkProposal(r, &p); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	out, err := s.Proposals.Create(r.Context(), p)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.record(r, audit.Event{Actor: actor(r), Action: "proposal.create", Subject: p.Kind + ":" + p.Target,
		Detail: map[string]any{"proposal": out.ID, "title": p.Title, "source": p.Source}})
	writeJSON(w, http.StatusCreated, out)
}

// checkProposal validates the proposed content up front and records which
// stored version it was made against (to catch stale proposals on accept).
func (s *Server) checkProposal(r *http.Request, p *proposals.Proposal) error {
	switch p.Kind {
	case "rule":
		rs, err := rules.ParseYAML([]byte(p.Body))
		if err != nil {
			return fmt.Errorf("YAML: %w", err)
		}
		if len(rs) != 1 {
			return fmt.Errorf("propose exactly one rule")
		}
		if rs[0].ID != p.Target {
			return fmt.Errorf("the proposal changes rule %q but the YAML has id %q", p.Target, rs[0].ID)
		}
		if err := s.validateRule(rs[0]); err != nil {
			return err
		}
		if st, err := s.RuleStore.Get(r.Context(), p.Target); err == nil {
			p.BaseVersion = st.Version
		}
	case "runbook":
		if _, err := runbooks.Parse(p.Body, runbooks.Meta{ID: p.Target}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("kind must be rule or runbook")
	}
	return nil
}

// decideProposal accepts (and applies) or rejects. The decider must be a
// named person other than the proposer: proposals get a second pair of eyes.
func (s *Server) decideProposal(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Note string `json:"note"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req)
		by := actor(r)
		if by == "anonymous" {
			writeErr(w, http.StatusForbidden, "set your name in \"Acting as\" before reviewing proposals")
			return
		}
		p, err := s.Proposals.Get(r.Context(), r.PathValue("id"))
		if errors.Is(err, proposals.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		self := strings.EqualFold(p.CreatedBy, by)
		if user := sessionUser(r); user != nil && p.CreatedByID != "" {
			self = user.ID == p.CreatedByID
		}
		if accept && self {
			writeErr(w, http.StatusForbidden, "someone other than the proposer must accept it")
			return
		}
		status := map[bool]string{true: "accepted", false: "rejected"}[accept]
		decided, err := s.Proposals.Decide(r.Context(), p.ID, status, by, req.Note)
		if errors.Is(err, proposals.ErrDecided) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		if accept {
			if err := s.applyProposal(r, decided, by); err != nil {
				_ = s.Proposals.Reopen(r.Context(), p.ID)
				writeErr(w, http.StatusUnprocessableEntity, "not applied: "+err.Error())
				return
			}
		}
		s.record(r, audit.Event{Actor: by, Action: "proposal." + status, Subject: p.Kind + ":" + p.Target,
			Detail: map[string]any{"proposal": p.ID, "proposer": p.CreatedBy, "note": req.Note}})
		writeJSON(w, http.StatusOK, decided)
	}
}

func (s *Server) applyProposal(r *http.Request, p *proposals.Proposal, by string) error {
	switch p.Kind {
	case "rule":
		rs, err := rules.ParseYAML([]byte(p.Body))
		if err != nil || len(rs) != 1 {
			return fmt.Errorf("invalid rule YAML")
		}
		if err := s.validateRule(rs[0]); err != nil {
			return err
		}
		// Made against a stored version: refuse if someone changed the rule since.
		if _, err := s.RuleStore.Put(r.Context(), rs[0], by+" (proposal by "+p.CreatedBy+")", p.BaseVersion); err != nil {
			if errors.Is(err, rules.ErrConflict) {
				return fmt.Errorf("the rule changed after this was proposed; propose again from the current version")
			}
			return err
		}
		s.notify(r, "rules")
		s.record(r, audit.Event{Actor: by, Action: "rule.update", Subject: "rule:" + p.Target, Detail: map[string]any{"proposal": p.ID}})
	case "runbook":
		rb, err := runbooks.Parse(p.Body, runbooks.Meta{ID: p.Target})
		if err != nil {
			return err
		}
		if _, err := s.RunbookStore.Put(r.Context(), runbooks.Stored{ID: rb.ID, Source: p.Body, Meta: runbooks.Meta{ID: rb.ID}}, by, 0); err != nil {
			return err
		}
		s.notify(r, "runbooks")
		s.record(r, audit.Event{Actor: by, Action: "runbook.update", Subject: "runbook:" + p.Target, Detail: map[string]any{"proposal": p.ID}})
	}
	return nil
}

// analyzeFatigue looks at how alerts were handled and files suggestions for
// noisy, flapping or ignored rules: POST /api/proposals/analyze {"days": 7}
func (s *Server) analyzeFatigue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Days      int `json:"days"`
		MinAlerts int `json:"minAlerts"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req)
	if req.Days <= 0 || req.Days > 90 {
		req.Days = 7
	}
	if req.MinAlerts <= 0 {
		req.MinAlerts = 5
	}
	stats, err := proposals.Analyze(r.Context(), s.Store.Pool, req.Days)
	if err != nil {
		s.fail(w, err)
		return
	}
	created := []*proposals.Proposal{}
	skipped := 0
	for _, sig := range proposals.Signals(stats, req.MinAlerts) {
		c := s.Engine.Current().ByID(sig.Rule)
		if c == nil {
			continue
		}
		rule, note, ok := suggestChange(c.Rule, sig.Name)
		if !ok {
			skipped++
			continue
		}
		if note != "" {
			sig.Rationale += " " + note
		}
		body, err := rules.ToYAML(rule)
		if err != nil {
			continue
		}
		p := proposals.Proposal{ID: event.NewID(), Kind: "rule", Target: sig.Rule, Title: sig.Title, Rationale: sig.Rationale,
			Body: string(body), Source: "system", Signal: sig.Name, CreatedBy: "kardan"}
		if err := s.checkProposal(r, &p); err != nil {
			s.Log.Warn("fatigue suggestion does not validate", "rule", sig.Rule, "err", err)
			continue
		}
		out, err := s.Proposals.Create(r.Context(), p)
		if errors.Is(err, proposals.ErrExists) {
			skipped++
			continue
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		created = append(created, out)
	}
	writeJSON(w, http.StatusOK, map[string]any{"rulesAnalyzed": len(stats), "created": created, "alreadyOpen": skipped, "days": req.Days})
}

var lower = map[string]string{"critical": "error", "error": "warning", "warning": "info"}

// suggestChange edits a copy of the rule for a fatigue signal; note explains
// a change that differs from the signal's usual fix.
func suggestChange(r rules.Rule, signal string) (rules.Rule, string, bool) {
	r.Origin, r.Overrides = "", ""
	note := ""
	switch signal {
	case "flapping":
		if r.Kind == rules.KindAggregate || r.Kind == rules.KindAbsence || r.Group != nil {
			return r, "", false
		}
		if r.AutoResolve == nil || r.AutoResolve.When == nil {
			r.Group = &rules.Group{By: []string{"$type"}, Window: "10m"}
			note = "Grouping matches into a 10-minute window sends one notification per window instead of one per blip."
			break
		}
		// Recovery-closed rules cannot be grouped: page less instead.
		note = "This rule closes alerts on recovery events, which rules out grouping, so the suggestion is one severity level lower: the alerts stay visible but stop paging people who use default preferences."
		signal = "noise"
		fallthrough
	case "noise", "ignored":
		cur := strings.ToLower(strings.TrimSpace(r.Presentation.Severity))
		if cur == "" || strings.Contains(cur, "{{") {
			cur = "error" // default follows the event; pin one level below a typical error
		}
		next, ok := lower[cur]
		if signal == "ignored" {
			next, ok = "info", cur != "info"
		}
		if !ok {
			return r, "", false
		}
		r.Presentation.Severity = next
		r.Presentation.Color = ""
	default:
		return r, "", false
	}
	return r, note, true
}
