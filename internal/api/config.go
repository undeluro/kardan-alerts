package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/undeluro/kardan-alerts/internal/audit"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/undeluro/kardan-alerts/internal/calendar"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/finance"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/schema"
	"github.com/undeluro/kardan-alerts/internal/tmpl"
)

// actor returns the authenticated identity when demo SSO is enabled.
func actor(r *http.Request) string {
	if user := sessionUser(r); user != nil {
		return user.Name
	}
	if v := strings.TrimSpace(r.Header.Get("X-User")); v != "" {
		return v
	}
	return "anonymous"
}

// decodeRule accepts JSON or YAML (Content-Type application/yaml, text/yaml,
// or any body that is not a JSON object).
func decodeRule(r *http.Request) (rules.Rule, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err != nil {
		return rules.Rule{}, err
	}
	body = bytes.TrimSpace(body)
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "yaml") || (len(body) > 0 && body[0] != '{') {
		rs, err := rules.ParseYAML(body)
		if err != nil {
			return rules.Rule{}, fmt.Errorf("YAML: %w", err)
		}
		if len(rs) != 1 {
			return rules.Rule{}, fmt.Errorf("expected exactly one rule, got %d", len(rs))
		}
		return rs[0], nil
	}
	var rule rules.Rule
	if err := json.Unmarshal(body, &rule); err != nil {
		return rules.Rule{}, fmt.Errorf("JSON: %w", err)
	}
	rule.Match.Normalize()
	if rule.AutoResolve != nil {
		rule.AutoResolve.When.Normalize()
	}
	return rule, nil
}

// validateRule compiles the rule and checks it against the directory, so a
// typo in a team or person is caught before the rule silently notifies nobody.
func (s *Server) validateRule(rule rules.Rule) error {
	c, err := rules.Compile(rule, s.DefaultWindow)
	if err != nil {
		return err
	}
	if s.CheckRule != nil {
		if err := s.CheckRule(c); err != nil {
			return err
		}
	}
	dir := s.Directory()
	for i, e := range rule.Escalation {
		if !dir.Exists(e.To) {
			return fmt.Errorf("escalation[%d].to %q matches no known team or person", i, e.To)
		}
	}
	if _, ok := dir.Team(rule.Team); !ok {
		return fmt.Errorf("unknown team %q", rule.Team)
	}
	for i, rt := range rule.Routing {
		if !dir.Exists(rt.To) {
			return fmt.Errorf("routing[%d].to %q matches no known team or person", i, rt.To)
		}
	}
	return nil
}

type ruleDetail struct {
	Rule      rules.Rule `json:"rule"`
	YAML      string     `json:"yaml"`
	Fields    []string   `json:"fields"`
	Version   int        `json:"version,omitempty"`
	UpdatedBy string     `json:"updatedBy,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

func (s *Server) detail(r rules.Rule, st *rules.Stored) ruleDetail {
	y, _ := rules.ToYAML(r)
	d := ruleDetail{Rule: r, YAML: string(y), Fields: rules.ReferencedFields(r.Match)}
	if st != nil {
		d.Version, d.UpdatedBy, d.UpdatedAt = st.Version, st.UpdatedBy, &st.UpdatedAt
	}
	return d
}

func (s *Server) getRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c := s.Engine.Current().ByID(id)
	st, err := s.RuleStore.Get(r.Context(), id)
	if err != nil && !errors.Is(err, rules.ErrNotFound) {
		s.fail(w, err)
		return
	}
	switch {
	case c != nil:
		writeJSON(w, http.StatusOK, s.detail(c.Rule, st))
	case st != nil: // stored but failed validation (shown so it can be fixed)
		writeJSON(w, http.StatusOK, s.detail(st.Rule, st))
	default:
		writeErr(w, http.StatusNotFound, "rule not found")
	}
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	rule, err := decodeRule(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.Engine.Current().ByID(rule.ID) != nil {
		writeErr(w, http.StatusConflict, fmt.Sprintf("a rule with id %q already exists; edit it instead", rule.ID))
		return
	}
	s.saveRule(w, r, rule, 0, http.StatusCreated)
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	rule, err := decodeRule(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	if rule.ID == "" {
		rule.ID = id
	}
	if rule.ID != id {
		writeErr(w, http.StatusBadRequest, "rule id cannot be changed; create a new rule instead")
		return
	}
	expect, _ := strconv.Atoi(strings.Trim(r.Header.Get("If-Match"), `"`))
	s.saveRule(w, r, rule, expect, http.StatusOK)
}

func (s *Server) saveRule(w http.ResponseWriter, r *http.Request, rule rules.Rule, expect, status int) {
	if err := s.validateRule(rule); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	st, err := s.RuleStore.Put(r.Context(), rule, actor(r), expect)
	if errors.Is(err, rules.ErrConflict) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.notify(r, "rules")
	s.Log.Info("rule saved", "rule", rule.ID, "by", actor(r), "version", st.Version)
	s.record(r, audit.Event{Actor: actor(r), Action: map[bool]string{true: "rule.create", false: "rule.update"}[st.Version == 1],
		Subject: "rule:" + rule.ID, Detail: map[string]any{"version": st.Version}})
	writeJSON(w, status, s.detail(st.Rule, st))
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.RuleStore.Delete(r.Context(), id)
	if errors.Is(err, rules.ErrNotFound) {
		if s.Engine.Current().ByID(id) != nil {
			writeErr(w, http.StatusConflict, "this rule comes from a file in config/rules; delete or edit the file")
			return
		}
		writeErr(w, http.StatusNotFound, "rule not found")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.notify(r, "rules")
	s.Log.Info("rule deleted", "rule", id, "by", actor(r))
	s.record(r, audit.Event{Actor: actor(r), Action: "rule.delete", Subject: "rule:" + id})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) notify(r *http.Request, kind string) {
	if s.Notify == nil {
		return
	}
	if err := s.Notify(r.Context(), kind); err != nil {
		s.Log.Warn("config change notice failed; replicas pick it up on the next version check", "err", err)
	}
}

// testRule dry-runs a rule against a sample event without saving anything:
//
//	POST /api/rules/test {"rule": {...} | "ruleYaml": "...", "event": {...} | "raw": "<any text>"}
//
// The event goes through the same parser auto-detection and schema facet
// mapping as live traffic, so the preview is what production would do.
func (s *Server) testRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule     *rules.Rule     `json:"rule"`
		RuleYAML string          `json:"ruleYaml"`
		Event    json.RawMessage `json:"event"`
		Raw      string          `json:"raw"`
		Source   string          `json:"source"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "body must be {rule|ruleYaml, event|raw}: "+err.Error())
		return
	}
	var rule rules.Rule
	switch {
	case req.RuleYAML != "":
		rs, err := rules.ParseYAML([]byte(req.RuleYAML))
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "YAML: " + err.Error()})
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
	raw := []byte(req.Raw)
	if len(req.Event) > 0 && string(req.Event) != "null" {
		raw = req.Event
	}
	src := req.Source
	if src == "" {
		src = "test"
	}
	env := envelopeFromRaw(s, src, raw)
	doc := schema.NewDoc(env)
	if s.FX != nil {
		doc.FX = s.FX()
	}
	if s.Schemas != nil {
		_, doc.Overrides, _ = s.Schemas.Identify(env.Payload)
	}

	fields := map[string]any{}
	for _, f := range rules.ReferencedFields(rule.Match) {
		v := doc.Get(f)
		if v.Exists() {
			fields[f] = v.Value()
		} else {
			fields[f] = nil
		}
	}
	matches := c.Matches(doc)
	resp := map[string]any{"valid": true, "matches": matches, "fields": fields, "format": env.Format,
		"payload": json.RawMessage(env.Payload)}
	rendered := c.Render(doc)
	resp["alert"] = rendered
	resp["fingerprint"] = c.Fingerprint(doc)
	usd := doc.Get("$notionalUsd")
	if usd.Exists() {
		resp["notionalUsd"] = usd.Num
	}
	resp["impact"] = finance.Impact(rendered.Severity, usd.Num, usd.Exists())
	if s.Correlation != nil {
		if f, v := s.Correlation().Key(doc); f != "" {
			resp["incidentKey"] = f + "=" + v
		}
	}
	if c.Grouped() {
		ws, we := c.WindowFor(env.ReceivedAt)
		resp["window"] = map[string]time.Time{"start": ws, "end": we}
	}
	s.explainTime(resp, c, doc, rendered.Severity, usd.Num, usd.Exists())
	writeJSON(w, http.StatusOK, resp)
}

// explainTime adds what the time-aware settings would do with this event right
// now (business clock): rule kind, market hours, cut-off and urgency.
func (s *Server) explainTime(resp map[string]any, c *rules.Compiled, doc *schema.Doc, sev string, usd float64, hasUSD bool) {
	resp["kind"] = c.Kind()
	now := time.Now()
	if s.Clock != nil {
		now = s.Clock.Now()
	}
	var cals *calendar.Set
	if s.Calendars != nil {
		cals = s.Calendars()
	}
	switch c.Kind() {
	case rules.KindAggregate:
		spec := c.Aggregate()
		values, _ := c.AggregateKey(doc)
		measure := "events"
		if spec.SumField != "" {
			measure = "sum of " + spec.SumField
		}
		resp["timeNote"] = fmt.Sprintf("Counts towards the window for %q: fires when the %s in %s reaches %s.",
			strings.Join(values, " / "), measure, rules.HumanWindow(spec.Window), tmpl.FormatNumber(spec.Threshold))
	case rules.KindAbsence:
		spec := c.Absence()
		key := c.AbsenceKey(doc)
		if spec.Deadline >= 0 {
			resp["timeNote"] = fmt.Sprintf("This is the expected event%s: it satisfies today's %02d:%02d deadline (%s).",
				forSender(key), spec.Deadline/60, spec.Deadline%60, spec.Calendar)
		} else {
			resp["timeNote"] = fmt.Sprintf("This is the expected heartbeat%s: it resets the %s timer.", forSender(key), rules.HumanWindow(spec.Every))
		}
	}
	if id, off := c.During(); id != "" {
		if cal, ok := cals.Get(id); ok {
			open := cal.IsOpen(now)
			resp["during"] = map[string]any{"calendar": id, "open": open, "otherwise": off}
		}
	}
	if mode := c.CutoffMode(); mode != "" {
		var d calendar.Deadline
		var ok bool
		if mode == "auto" {
			d, ok = cals.NextCutoff(doc.Get("$currency").String(), now)
		} else {
			d, ok = cals.Named(mode, now)
		}
		if ok {
			u := finance.Urgency(d.At, now)
			resp["cutoff"] = map[string]any{"name": d.Name + " (" + d.Calendar + ")", "at": d.At,
				"minutesLeft": int(d.At.Sub(now).Minutes()), "urgency": u}
			resp["impact"] = min(100, finance.Impact(sev, usd, hasUSD)+u)
		}
	}
}

func forSender(key string) string {
	if key == "" {
		return ""
	}
	return " from " + key
}

func envelopeFromRaw(s *Server, source string, raw []byte) *event.Envelope {
	if s.ParseRaw != nil {
		return s.ParseRaw(source, raw)
	}
	return &event.Envelope{ID: "test", SourceID: source, ReceivedAt: time.Now().UTC(), Payload: raw, Format: "json"}
}

// ---- schemas -------------------------------------------------------------------

func (s *Server) listSchemas(w http.ResponseWriter, r *http.Request) {
	list, err := s.Schemas.List(r.Context(), 200)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, sc := range list {
		sc.Sample = nil
		sc.SampleRaw = ""
	}
	writeJSON(w, http.StatusOK, list)
}

type schemaDetail struct {
	*schema.Schema
	Facets      map[string]string `json:"facets"`
	FacetValues map[string]any    `json:"facetValues"`
}

func (s *Server) schemaDetail(sc *schema.Schema) schemaDetail {
	facets := sc.Facets()
	doc := schema.NewDoc(&event.Envelope{Payload: sc.Sample, SourceID: firstOr(sc.Sources, ""), Format: sc.Format,
		Raw: sc.SampleRaw})
	doc.Overrides = facets
	if s.FX != nil {
		doc.FX = s.FX()
	}
	values := map[string]any{}
	for _, f := range schema.FacetNames() {
		switch f {
		case "$id", "$receivedAt", "$raw":
			continue
		}
		if v := doc.Get(f); v.Exists() {
			values[f] = v.Value()
		}
	}
	return schemaDetail{Schema: sc, Facets: facets, FacetValues: values}
}

func (s *Server) getSchema(w http.ResponseWriter, r *http.Request) {
	sc, err := s.Schemas.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, schema.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "schema not found")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.schemaDetail(sc))
}

// updateSchema renames a schema and sets facet mappings by hand, e.g.
// {"facets": {"$severity": "criticity"}} for a schema with an unusual field.
func (s *Server) updateSchema(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string            `json:"name"`
		Facets map[string]string `json:"facets"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Facets == nil {
		req.Facets = map[string]string{}
	}
	sc, err := s.Schemas.SetManualFacets(r.Context(), r.PathValue("id"), strings.TrimSpace(req.Name), req.Facets)
	if errors.Is(err, schema.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "schema not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.notify(r, "schemas")
	s.record(r, audit.Event{Actor: actor(r), Action: "schema.update", Subject: "schema:" + sc.ID,
		Detail: map[string]any{"name": req.Name, "facets": req.Facets}})
	s.Log.Info("schema facets updated", "schema", sc.ID, "by", actor(r))
	writeJSON(w, http.StatusOK, s.schemaDetail(sc))
}

func (s *Server) listSources(w http.ResponseWriter, _ *http.Request) {
	if s.Sources == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, s.Sources())
}

func firstOr(xs []string, d string) string {
	if len(xs) > 0 {
		return xs[0]
	}
	return d
}
