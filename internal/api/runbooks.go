package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/jackc/pgx/v5"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/audit"
	"github.com/undeluro/kardan-alerts/internal/runbooks"
	"github.com/undeluro/kardan-alerts/internal/runner"
)

func (s *Server) registerRunbooks(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/runbooks", s.listRunbooks)
	mux.HandleFunc("POST /api/runbooks", s.saveRunbook)
	mux.HandleFunc("POST /api/runbooks/parse", s.parseRunbook)
	mux.HandleFunc("GET /api/runbooks/{id}", s.getRunbook)
	mux.HandleFunc("PUT /api/runbooks/{id}", s.saveRunbook)
	mux.HandleFunc("DELETE /api/runbooks/{id}", s.deleteRunbook)
	mux.HandleFunc("GET /api/actions", s.listActions)
	mux.HandleFunc("GET /api/alerts/{id}/runbooks", s.alertRunbooks)
	mux.HandleFunc("POST /api/alerts/{id}/runbooks/{rb}/steps/{n}", s.requestStep)
	mux.HandleFunc("POST /api/runs/{id}/approve", s.decide(true))
	mux.HandleFunc("POST /api/runs/{id}/reject", s.decide(false))
	mux.HandleFunc("GET /approve/{token}", s.approvePage)
	mux.HandleFunc("POST /approve/{token}", s.approvePage)
	mux.HandleFunc("GET /api/audit", s.listAudit)
	mux.HandleFunc("GET /api/audit/verify", s.verifyAudit)
	mux.HandleFunc("GET /api/alerts/{id}/timeline", s.alertTimeline)
	mux.HandleFunc("GET /api/alerts/{id}/report", s.alertReport)
}

// ---- catalogue -----------------------------------------------------------------

type runbookSummary struct {
	*runbooks.Runbook
	Editable bool `json:"editable"` // stored in the database (file runbooks are edited in config/runbooks)
}

func (s *Server) listRunbooks(w http.ResponseWriter, _ *http.Request) {
	cat := s.Runbooks.Current()
	out := make([]runbookSummary, 0, len(cat.Runbooks))
	for _, rb := range cat.Runbooks {
		out = append(out, runbookSummary{Runbook: rb, Editable: rb.Origin == "api"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"runbooks": out, "errors": nonNil(cat.Errors), "loadedAt": cat.LoadedAt})
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func (s *Server) getRunbook(w http.ResponseWriter, r *http.Request) {
	rb, ok := s.Runbooks.Current().Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "runbook not found")
		return
	}
	resp := map[string]any{"runbook": rb, "yaml": runbooks.ToYAML(rb), "editable": rb.Origin == "api",
		"checks": s.stepChecks(rb)}
	if rb.Origin == "api" {
		if rows, err := s.RunbookStore.List(r.Context()); err == nil {
			for _, st := range rows {
				if st.ID == rb.ID {
					resp["version"], resp["updatedBy"], resp["updatedAt"], resp["meta"] = st.Version, st.UpdatedBy, st.UpdatedAt, st.Meta
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// stepChecks explains, per step, what Kardan will do with it.
func (s *Server) stepChecks(rb *runbooks.Runbook) []map[string]any {
	reg := s.Actions()
	out := make([]map[string]any, len(rb.Steps))
	for i, st := range rb.Steps {
		c := map[string]any{"index": i}
		if st.Action != "" {
			if d, ok := reg.Get(st.Action); ok {
				c["label"], c["risk"] = d.Label, d.Risk
				c["needs"] = map[bool]string{true: "approval", false: "none"}[reg.Approval(st.Action, st.Approval) == "required"]
				if err := reg.Validate(st.Action, withEscalateTeam(st.Action, st.Params)); err != nil {
					c["problem"] = err.Error()
				}
			} else {
				c["problem"] = fmt.Sprintf("unknown action %q: shown as text (add it to config/actions.yaml)", st.Action)
			}
		}
		out[i] = c
	}
	return out
}

func withEscalateTeam(action string, p map[string]string) map[string]string {
	if action != "escalate" || p["to"] != "" || p["team"] == "" {
		return p
	}
	cp := map[string]string{"to": "team:" + p["team"]}
	for k, v := range p {
		cp[k] = v
	}
	return cp
}

func (s *Server) listActions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Actions().List())
}

// ---- create, edit, preview -----------------------------------------------------

type runbookRequest struct {
	Source  string   `json:"source"`
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Team    string   `json:"team"`
	Format  string   `json:"format"`
	Matches any      `json:"matches"` // {field: value} or a condition; or YAML text
	Rules   []string `json:"rules"`
	Auto    bool     `json:"auto"`
	Version int      `json:"version"`
}

func decodeRunbookRequest(r *http.Request) (runbookRequest, runbooks.Meta, error) {
	var req runbookRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err != nil {
		return req, runbooks.Meta{}, err
	}
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, `"source"`) {
		if err := json.Unmarshal(body, &req); err != nil {
			return req, runbooks.Meta{}, err
		}
	} else {
		req.Source = string(body) // raw text: front matter or YAML keys carry the metadata
		q := r.URL.Query()
		req.ID, req.Team, req.Format = q.Get("id"), q.Get("team"), q.Get("format")
	}
	meta := runbooks.Meta{ID: strings.TrimSpace(req.ID), Name: req.Name, Team: req.Team, Format: req.Format,
		Rules: req.Rules, Auto: req.Auto}
	switch m := req.Matches.(type) {
	case nil:
	case string:
		if strings.TrimSpace(m) != "" {
			var v any
			if err := yaml.Unmarshal([]byte(m), &v); err != nil {
				return req, meta, fmt.Errorf("matches: %w", err)
			}
			c, err := runbooks.MatchesCondition(v)
			if err != nil {
				return req, meta, fmt.Errorf("matches: %w", err)
			}
			meta.Matches = c
		}
	default:
		c, err := runbooks.MatchesCondition(m)
		if err != nil {
			return req, meta, fmt.Errorf("matches: %w", err)
		}
		meta.Matches = c
	}
	return req, meta, nil
}

// parseRunbook previews how Kardan reads a runbook: format, steps, actions
// and approvals, plus a structured YAML version to correct and save.
func (s *Server) parseRunbook(w http.ResponseWriter, r *http.Request) {
	req, meta, err := decodeRunbookRequest(r)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	if meta.ID == "" {
		meta.ID = "draft"
	}
	rb, err := runbooks.Parse(req.Source, meta)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error(), "detected": runbooks.Detect(req.Source)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "runbook": rb, "yaml": runbooks.ToYAML(rb), "checks": s.stepChecks(rb)})
}

func (s *Server) saveRunbook(w http.ResponseWriter, r *http.Request) {
	req, meta, err := decodeRunbookRequest(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pathID := r.PathValue("id")
	if pathID != "" && meta.ID == "" {
		meta.ID = pathID
	}
	rb, err := runbooks.Parse(req.Source, meta)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if pathID != "" && rb.ID != pathID {
		writeErr(w, http.StatusBadRequest, "the runbook id cannot be changed; create a new runbook instead")
		return
	}
	if pathID == "" {
		if existing, ok := s.Runbooks.Current().Get(rb.ID); ok && existing.Origin == "api" {
			writeErr(w, http.StatusConflict, fmt.Sprintf("a runbook with id %q already exists; edit it instead", rb.ID))
			return
		}
	}
	if rb.Team != "" {
		if _, ok := s.Directory().Team(rb.Team); !ok {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": fmt.Sprintf("unknown team %q", rb.Team)})
			return
		}
	}
	meta.ID = rb.ID
	st, err := s.RunbookStore.Put(r.Context(), runbooks.Stored{ID: rb.ID, Source: req.Source, Meta: meta}, actor(r), req.Version)
	if errors.Is(err, runbooks.ErrVersionConflict) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.record(r, audit.Event{Actor: actor(r), Action: map[bool]string{true: "runbook.create", false: "runbook.update"}[st.Version == 1],
		Subject: "runbook:" + rb.ID, Detail: map[string]any{"version": st.Version, "format": rb.Format, "steps": len(rb.Steps)}})
	s.notify(r, "runbooks")
	status := http.StatusOK
	if pathID == "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"runbook": rb, "version": st.Version, "yaml": runbooks.ToYAML(rb), "checks": s.stepChecks(rb)})
}

func (s *Server) deleteRunbook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ok, err := s.RunbookStore.Delete(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		if _, exists := s.Runbooks.Current().Get(id); exists {
			writeErr(w, http.StatusConflict, "this runbook comes from a file in config/runbooks; edit or delete the file")
			return
		}
		writeErr(w, http.StatusNotFound, "runbook not found")
		return
	}
	s.record(r, audit.Event{Actor: actor(r), Action: "runbook.delete", Subject: "runbook:" + id})
	s.notify(r, "runbooks")
	w.WriteHeader(http.StatusNoContent)
}

// record appends to the audit trail outside a transaction (best effort: a
// failure is logged, the change it describes already happened).
func (s *Server) record(r *http.Request, ev audit.Event) {
	if s.Audit == nil {
		return
	}
	if _, err := s.Audit.Record(r.Context(), ev); err != nil {
		s.Log.Error("audit append failed", "action", ev.Action, "subject", ev.Subject, "err", err)
	}
}

// ---- runbooks on an alert ------------------------------------------------------

func (s *Server) alertRunbooks(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	views, err := s.Runner.Views(r.Context(), a)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

// requestStep runs a step for the acting person: a manual step is marked
// done, an action is queued or sent for approval.
func (s *Server) requestStep(w http.ResponseWriter, r *http.Request) {
	var body struct {
		By   string `json:"by"`
		Skip string `json:"skip"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	by := actionActor(r, body.By)
	opts := runner.RequestOptions{Skip: strings.TrimSpace(body.Skip)}
	if user := sessionUser(r); user != nil {
		by = user.ID
		opts.RequesterID = user.ID
	}
	if p, ok := s.Directory().Find(by); ok {
		by = p.Name
	} else {
		writeErr(w, http.StatusForbidden, runner.ErrUnknownPerson.Error())
		return
	}
	a, err := s.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !s.canActOnTeam(r, a.Team) {
		writeErr(w, http.StatusForbidden, "you are not a member of the alert's team")
		return
	}
	if a.Status == alerting.StatusResolved {
		writeErr(w, http.StatusConflict, "the alert is resolved")
		return
	}
	rb, ok := s.Runbooks.Current().Get(r.PathValue("rb"))
	if !ok {
		writeErr(w, http.StatusNotFound, "runbook not found")
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "step must be a number")
		return
	}
	var run *alerting.Run
	err = pgx.BeginFunc(r.Context(), s.Store.Pool, func(tx pgx.Tx) error {
		run, err = s.Runner.Request(r.Context(), tx, a, rb, n, by, opts)
		return err
	})
	switch {
	case errors.Is(err, runner.ErrInProgress), errors.Is(err, runner.ErrNotRunnable):
		writeErr(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, runner.ErrUnknownStep):
		writeErr(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.Log.Info("runbook step requested", "alert", a.ID, "runbook", rb.ID, "step", n+1, "by", by, "status", run.Status)
	s.publishRunbook(r, a.ID)
	writeJSON(w, http.StatusCreated, run)
}

// decide approves or rejects a step, enforcing four-eyes on the server.
func (s *Server) decide(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			By     string `json:"by"`
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
		by := actionActor(r, body.By)
		run, status, err := s.decideRun(r, r.PathValue("id"), by, approve, body.Reason, "ui")
		if err != nil {
			writeErr(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, run)
	}
}

// decideRun is shared by the UI, SMS replies and email links.
func (s *Server) decideRun(r *http.Request, runID, by string, approve bool, reason, via string) (*alerting.Run, int, error) {
	ctx := r.Context()
	run, err := alerting.GetRun(ctx, s.Store.Pool, runID)
	if err != nil {
		if errors.Is(err, alerting.ErrNotFound) {
			return nil, http.StatusNotFound, errors.New("no such step")
		}
		return nil, http.StatusInternalServerError, err
	}
	a, err := s.Store.Get(ctx, run.AlertID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if a.Status == alerting.StatusResolved {
		return nil, http.StatusConflict, errors.New("the alert is already resolved")
	}
	if !s.canActOnTeam(r, a.Team) {
		return nil, http.StatusForbidden, errors.New("you are not a member of the alert's team")
	}
	approver := by
	if user := sessionUser(r); user != nil {
		approver = user.ID
	}
	p, err := runner.CheckApprover(s.Directory(), run, a.Team, approver)
	if err != nil {
		s.record(r, audit.Event{Actor: by, Action: "action.approval_refused", Subject: "run:" + run.ID,
			Detail: map[string]any{"alert": a.ID, "reason": err.Error(), "via": via}})
		return nil, http.StatusForbidden, err
	}
	var out *alerting.Run
	err = pgx.BeginFunc(ctx, s.Store.Pool, func(tx pgx.Tx) error {
		var err error
		if approve {
			out, err = alerting.ApproveRun(ctx, tx, run.ID, p.Name)
		} else {
			out, err = alerting.RejectRun(ctx, tx, run.ID, p.Name, strings.TrimSpace(reason))
		}
		if err != nil {
			return err
		}
		what := map[bool]string{true: "action.approved", false: "action.rejected"}[approve]
		_, err = audit.Append(ctx, tx, audit.Event{Actor: p.Name, Action: what, Subject: "run:" + run.ID,
			Detail: map[string]any{"alert": a.ID, "label": run.Label, "requestedBy": run.RequestedBy, "risk": run.Risk,
				"via": via, "reason": reason}})
		return err
	})
	if errors.Is(err, alerting.ErrNotAwaiting) {
		return nil, http.StatusConflict, err
	}
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	s.Log.Info("runbook step decided", "run", run.ID, "approved", approve, "by", p.Name, "via", via)
	s.publishRunbook(r, a.ID)
	return out, http.StatusOK, nil
}

func (s *Server) publishRunbook(r *http.Request, alertID string) {
	a, err := s.Store.Get(r.Context(), alertID)
	if err != nil {
		return
	}
	if n, err := alerting.PendingApprovals(r.Context(), s.Store.Pool, []string{a.ID}); err == nil {
		a.PendingApprovals = n[a.ID]
	}
	s.publishStatus(r, a)
}

// ---- one-click approval links (email) ------------------------------------------

var approveTmpl = template.Must(template.New("approve").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Kardan approval</title>
<style>body{font-family:-apple-system,Segoe UI,Roboto,sans-serif;background:#f6f7f9;margin:0;padding:24px;color:#111827}
.card{max-width:560px;margin:auto;background:#fff;border:1px solid #e5e7eb;border-radius:8px;padding:24px}
.b{display:inline-block;padding:10px 16px;border-radius:6px;border:0;font-size:15px;cursor:pointer;margin-right:8px}
.ok{background:#16a34a;color:#fff}.no{background:#fff;color:#b91c1c;border:1px solid #fecaca}
.muted{color:#6b7280;font-size:13px}.done{padding:12px;border-radius:6px;background:#f0fdf4;color:#166534}
.err{padding:12px;border-radius:6px;background:#fef2f2;color:#991b1b}
@media (prefers-color-scheme:dark){body{background:#030712;color:#f3f4f6}.card{background:#111827;border-color:#1f2937}.no{background:#111827}}</style></head>
<body><div class="card">
<div class="muted">Kardan · four-eyes approval</div>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
{{if .Done}}<p class="done">{{.Done}}</p>{{end}}
{{if .Run}}<h1 style="font-size:20px">{{.Run.Label}}</h1>
<p>For the alert <a href="{{.AlertLink}}">{{.AlertTitle}}</a>.<br>Requested by <b>{{.Run.RequestedBy}}</b> · {{.Run.Risk}} risk · status <b>{{.Run.Status}}</b></p>
{{if .CanDecide}}<p>You are approving as <b>{{.Person}}</b>.</p>
<form method="post"><input type="hidden" name="decision" value="{{.Decision}}">
{{if eq .Decision "reject"}}<p><input name="reason" placeholder="Reason (optional)" style="width:100%;padding:8px"></p>
<button class="b no" type="submit">Reject this step</button>{{else}}
<button class="b ok" type="submit">Approve this step</button>{{end}}
<a class="muted" href="{{.AlertLink}}">Open the alert instead</a></form>{{end}}{{end}}
</div></body></html>`))

// approvePage serves the confirmation page behind a signed email link (GET)
// and records the decision (POST). The link names the person, so the
// four-eyes check runs exactly as for UI and SMS approvals.
func (s *Server) approvePage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{}
	render := func(status int) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = approveTmpl.Execute(w, data)
	}
	runID, personID, err := runner.VerifyLink(s.ApprovalSecret, r.PathValue("token"), time.Now())
	if err != nil {
		data["Error"] = err.Error()
		render(http.StatusForbidden)
		return
	}
	run, err := alerting.GetRun(r.Context(), s.Store.Pool, runID)
	if err != nil {
		data["Error"] = "This step no longer exists."
		render(http.StatusNotFound)
		return
	}
	person, ok := s.Directory().Person(personID)
	if !ok {
		data["Error"] = "Unknown person."
		render(http.StatusForbidden)
		return
	}
	decision := r.URL.Query().Get("decision")
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		decision = r.PostFormValue("decision")
		out, status, err := s.decideRun(r, run.ID, person.ID, decision != "reject", r.PostFormValue("reason"), "email link")
		if err != nil {
			data["Error"] = err.Error()
		} else {
			run = out
			data["Done"] = map[bool]string{true: "Approved. Kardan runs the step now; the alert page shows the result.",
				false: "Rejected. The step will not run."}[decision != "reject"]
		}
		defer render(status)
	} else {
		defer render(http.StatusOK)
	}
	a, _ := s.Store.Get(r.Context(), run.AlertID)
	if decision != "reject" {
		decision = "approve"
	}
	data["Run"], data["Person"], data["Decision"] = run, person.Name, decision
	data["CanDecide"] = run.Status == alerting.RunAwaitingApproval && data["Done"] == nil
	if a != nil {
		data["AlertTitle"], data["AlertLink"] = a.Title, "/alerts/"+a.ID
	}
}

// ---- audit ---------------------------------------------------------------------

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, err := s.Audit.List(r.Context(), audit.Filter{Subject: q.Get("subject"), Prefix: q.Get("prefix"), Limit: limit})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	v, err := s.Audit.Verify(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// alertTimeline is the audit trail of an alert and its runbook steps.
func (s *Server) alertTimeline(w http.ResponseWriter, r *http.Request) {
	entries, err := s.timeline(r, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) timeline(r *http.Request, alertID string) ([]audit.Entry, error) {
	runs, err := alerting.RunsForAlert(r.Context(), s.Store.Pool, alertID)
	if err != nil {
		return nil, err
	}
	subjects := []string{"alert:" + alertID}
	for _, run := range runs {
		subjects = append(subjects, "run:"+run.ID)
	}
	return s.Audit.ForSubjects(r.Context(), subjects)
}
