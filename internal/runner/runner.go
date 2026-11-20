// Package runner connects runbooks to alerts: which runbooks apply, what
// their steps say with the alert's values filled in, and how a step becomes
// an action run (queued, or waiting for a second person's approval). The API
// uses it when people click; the worker uses it to auto-run runbooks and to
// move on after each step.
package runner

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/undeluro/kardan-alerts/internal/actions"
	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/audit"
	"github.com/undeluro/kardan-alerts/internal/directory"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/finance"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/runbooks"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

// System is the actor for steps Kardan runs on its own.
const System = "kardan"

type Runner struct {
	Pool      *pgxpool.Pool
	Catalog   *runbooks.Holder
	Actions   func() *actions.Registry
	Engine    *rules.Engine
	Schemas   *schema.Registry
	FX        func() *finance.FX
	Directory func() *directory.Directory
}

// Doc rebuilds an alert's event document (first event, facet mappings,
// computed values) plus the alert's own $ values for step templates.
func (r *Runner) Doc(a *alerting.Alert) (*schema.Doc, string) {
	env := &event.Envelope{ID: a.FirstEventID, SourceID: a.SourceID, ReceivedAt: a.FirstSeen, Payload: a.Sample,
		Raw: a.SampleRaw, Format: a.SampleFormat}
	doc := schema.NewDoc(env)
	if r.FX != nil {
		doc.FX = r.FX()
	}
	sig := ""
	if r.Schemas != nil && len(a.Sample) > 0 {
		sig, doc.Overrides, _ = r.Schemas.Identify(a.Sample)
	}
	vars := runbooks.AlertVars(a.ID, a.Title, a.Team, a.Severity, string(a.Status), alerting.ShortCode(a.ID))
	for k, v := range schema.DecodeVars(a.Vars) {
		vars[k] = v
	}
	return doc.WithVars(vars), sig
}

// For lists the runbooks attached to an alert.
func (r *Runner) For(a *alerting.Alert, doc *schema.Doc, sig string) []*runbooks.Runbook {
	t := runbooks.Target{RuleID: a.RuleID, SchemaID: sig, Doc: doc}
	if c := r.Engine.Current().ByID(a.RuleID); c != nil {
		t.RuleRunbooks = c.Rule.Runbooks
	}
	return r.Catalog.Current().For(t)
}

// StepView is a filled step with its latest run and what it would take to run.
type StepView struct {
	runbooks.Filled
	Label    string          `json:"label"`             // what it does, values filled in
	Risk     string          `json:"risk,omitempty"`    // of the action
	Needs    string          `json:"needs"`             // none | approval
	Runnable bool            `json:"runnable"`          // an action Kardan can execute
	Problem  string          `json:"problem,omitempty"` // why it cannot run (unknown action, missing value)
	Run      *alerting.Run   `json:"run,omitempty"`     // latest execution
	History  []*alerting.Run `json:"history,omitempty"` // earlier executions
}

// View is a runbook as it applies to one alert.
type View struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Format  string     `json:"format"`
	URL     string     `json:"url,omitempty"`
	Summary string     `json:"summary,omitempty"`
	Auto    bool       `json:"auto"`
	Origin  string     `json:"origin,omitempty"`
	Why     string     `json:"why"` // how it is attached
	Steps   []StepView `json:"steps"`
}

// Views renders every attached runbook with the alert's values and runs.
func (r *Runner) Views(ctx context.Context, a *alerting.Alert) ([]View, error) {
	doc, sig := r.Doc(a)
	list := r.For(a, doc, sig)
	runs, err := alerting.RunsForAlert(ctx, r.Pool, a.ID)
	if err != nil {
		return nil, err
	}
	reg := r.Actions()
	rule := r.Engine.Current().ByID(a.RuleID)
	out := make([]View, 0, len(list))
	for _, rb := range list {
		v := View{ID: rb.ID, Name: rb.Name, Format: rb.Format, URL: rb.URL, Summary: rb.Summary, Auto: rb.Auto,
			Origin: rb.Origin, Why: why(rb, a, rule)}
		for _, f := range rb.Fill(doc.Get) {
			sv := r.stepView(reg, f)
			for _, run := range runs {
				if run.RunbookID == rb.ID && run.Step == f.Index {
					if sv.Run != nil {
						sv.History = append(sv.History, sv.Run)
					}
					sv.Run = run
				}
			}
			v.Steps = append(v.Steps, sv)
		}
		out = append(out, v)
	}
	return out, nil
}

func why(rb *runbooks.Runbook, a *alerting.Alert, rule *rules.Compiled) string {
	if rule != nil {
		for _, id := range rule.Rule.Runbooks {
			if id == rb.ID {
				return "rule " + a.RuleID + " lists it"
			}
		}
	}
	for _, id := range rb.Rules {
		if id == a.RuleID {
			return "attached to rule " + a.RuleID
		}
	}
	if len(rb.Schemas) > 0 {
		return "attached to this event's schema"
	}
	return "its matches condition fits this event"
}

func (r *Runner) stepView(reg *actions.Registry, f runbooks.Filled) StepView {
	sv := StepView{Filled: f, Label: f.Text, Needs: "none"}
	if f.Action == "" {
		return sv
	}
	params := actionParams(f)
	sv.Risk = reg.Risk(f.Action)
	if reg.Approval(f.Action, f.Approval) == "required" {
		sv.Needs = "approval"
	}
	if sv.Label == "" {
		sv.Label = reg.Label(f.Action, params)
	}
	if err := reg.Validate(f.Action, params); err != nil {
		sv.Problem = err.Error()
	} else if len(f.Missing) > 0 {
		sv.Problem = "this alert has no value for " + strings.Join(f.Missing, ", ")
	} else {
		sv.Runnable = true
	}
	return sv
}

// actionParams adapts runbook shorthand to the action's parameters
// (Appendix B writes "team: counterparty-support" for escalate).
func actionParams(f runbooks.Filled) map[string]string {
	p := map[string]string{}
	for k, v := range f.Params {
		p[k] = v
	}
	if f.Action == "escalate" && p["to"] == "" && p["team"] != "" {
		p["to"] = "team:" + p["team"]
	}
	return p
}

// ---- requesting steps ----------------------------------------------------------

var (
	ErrInProgress  = errors.New("this step is already in progress")
	ErrNotRunnable = errors.New("this step cannot run")
	ErrUnknownStep = errors.New("no such runbook step for this alert")
)

// Request starts a step on an alert for an actor: a manual step is marked
// done, an action is queued, or waits for approval if it needs one.
func (r *Runner) Request(ctx context.Context, tx pgx.Tx, a *alerting.Alert, rb *runbooks.Runbook, step int,
	actor string, opts RequestOptions) (*alerting.Run, error) {
	if step < 0 || step >= len(rb.Steps) {
		return nil, ErrUnknownStep
	}
	doc, _ := r.Doc(a)
	filled := rb.Fill(doc.Get)
	f := filled[step]
	runs, err := alerting.RunsForAlert(ctx, tx, a.ID)
	if err != nil {
		return nil, err
	}
	for _, x := range runs {
		if x.RunbookID == rb.ID && x.Step == step && !x.Final() {
			return nil, ErrInProgress
		}
	}
	run := &alerting.Run{ID: event.NewID(), AlertID: a.ID, RunbookID: rb.ID, Step: step, Kind: f.Kind,
		Label: f.Text, RequestedBy: actor, Risk: actions.RiskLow, Approval: "none"}
	reg := r.Actions()
	if f.Action == "" {
		// Manual: someone did it (or confirms the check).
		run.Status, run.DecidedBy = alerting.RunDone, &actor
		if opts.Skip != "" {
			run.Status, run.Reason = alerting.RunSkipped, opts.Skip
		}
	} else {
		sv := r.stepView(reg, f)
		if !sv.Runnable {
			if opts.Skip == "" {
				return nil, fmt.Errorf("%w: %s", ErrNotRunnable, sv.Problem)
			}
		}
		run.Action, run.Params, run.Risk = f.Action, actionParams(f), sv.Risk
		if run.Label == "" {
			run.Label = sv.Label
		}
		run.Approval = reg.Approval(f.Action, f.Approval)
		switch {
		case opts.Skip != "":
			run.Status, run.Reason = alerting.RunSkipped, opts.Skip
		case run.Approval == "required":
			run.Status = alerting.RunAwaitingApproval
		default:
			run.Status = alerting.RunQueued
		}
		if f.Action == "wait" {
			if d, err := runbooks.ParseDuration(run.Params["duration"]); err == nil {
				run.NotBefore = time.Now().Add(d)
			}
		}
		// "if retry fails twice" on the next step: try this one twice.
		if step+1 < len(rb.Steps) && runbooks.OnFailure(rb.Steps[step+1].If) {
			run.MaxAttempts = max(1, runbooks.Attempts(rb.Steps[step+1].If))
		}
		if f.Every != "" && run.Risk != actions.RiskHigh {
			if d, err := runbooks.ParseDuration(f.Every); err == nil && d >= time.Minute {
				run.RepeatEvery = d.Milliseconds()
			}
		}
	}
	if err := alerting.InsertRun(ctx, tx, run); err != nil {
		return nil, err
	}
	action := map[string]string{alerting.RunDone: "runbook.step.done", alerting.RunSkipped: "runbook.step.skipped",
		alerting.RunAwaitingApproval: "runbook.step.approval_requested", alerting.RunQueued: "runbook.step.requested"}[run.Status]
	detail := map[string]any{"alert": a.ID, "runbook": rb.ID, "step": step + 1, "label": run.Label,
		"action": run.Action, "params": run.Params, "risk": run.Risk, "approval": run.Approval}
	if run.Reason != "" {
		detail["reason"] = run.Reason
	}
	if _, err := audit.Append(ctx, tx, audit.Event{Actor: actor, Action: action, Subject: "run:" + run.ID, Detail: detail}); err != nil {
		return nil, err
	}
	return run, nil
}

// RequestOptions tweaks a request.
type RequestOptions struct {
	Skip string // record the step as skipped with this reason instead of running it
}

// ---- auto-run progression ------------------------------------------------------

// Advance runs an auto runbook forward from a step: actions that need no
// approval are queued one at a time, approvals are requested, conditional
// steps run only when their condition holds ("if retry fails twice" after a
// failure, skipped after a success), and manual steps stop the run for people.
// prev is the outcome of the step before ("" at the start).
func (r *Runner) Advance(ctx context.Context, tx pgx.Tx, a *alerting.Alert, rb *runbooks.Runbook, from int, prev string) error {
	if !rb.Auto || a.Status == alerting.StatusResolved {
		return nil
	}
	for i := from; i < len(rb.Steps); i++ {
		s := rb.Steps[i]
		if s.If != "" {
			if !runbooks.OnFailure(s.If) {
				return nil // a judgement call ("if one region"): leave it to people
			}
			if prev != alerting.RunFailed {
				if _, err := r.Request(ctx, tx, a, rb, i, System, RequestOptions{Skip: "condition not met: " + s.If}); err != nil &&
					!errors.Is(err, ErrInProgress) {
					return err
				}
				continue
			}
		}
		if s.Action == "" {
			return nil // manual step: people take it from here
		}
		_, err := r.Request(ctx, tx, a, rb, i, System, RequestOptions{})
		if errors.Is(err, ErrInProgress) {
			return nil
		}
		if errors.Is(err, ErrNotRunnable) {
			return nil // e.g. a value missing from this alert: people decide
		}
		return err // queued or waiting for approval: continue when it finishes
	}
	return nil
}

// ---- four-eyes -----------------------------------------------------------------

var (
	ErrUnknownPerson = errors.New("approvals need a known person: choose who you are in \"Acting as\"")
	ErrSelfApproval  = errors.New("four-eyes: the person who requested this step cannot approve it")
	ErrNotEntitled   = errors.New("high-risk steps can only be approved by a member of the alert's team")
)

// CheckApprover enforces the four-eyes rule: a known person, not the
// requester, and for high-risk actions a member of the alert's team.
func CheckApprover(dir *directory.Directory, run *alerting.Run, team, approver string) (directory.Person, error) {
	p, ok := dir.Find(approver)
	if !ok {
		return p, ErrUnknownPerson
	}
	if req, ok := dir.Find(run.RequestedBy); ok && req.ID == p.ID {
		return p, ErrSelfApproval
	}
	if strings.EqualFold(run.RequestedBy, p.Name) || strings.EqualFold(run.RequestedBy, p.ID) {
		return p, ErrSelfApproval
	}
	if run.Risk == actions.RiskHigh && !dir.IsMember(team, p.ID) {
		return p, ErrNotEntitled
	}
	return p, nil
}

// ---- signed approval links -----------------------------------------------------

// SignLink makes a one-click approval token for an email: run, person and
// expiry, signed with HMAC-SHA256. The run's state makes it single-use.
func SignLink(secret, runID, personID string, exp time.Time) string {
	payload := runID + "." + personID + "." + strconv.FormatInt(exp.Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// VerifyLink checks a token and returns the run and person it is for.
func VerifyLink(secret, token string, now time.Time) (runID, personID string, err error) {
	p, s, ok := strings.Cut(token, ".")
	if !ok {
		return "", "", errors.New("malformed link")
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	sig, err2 := base64.RawURLEncoding.DecodeString(s)
	if err1 != nil || err2 != nil {
		return "", "", errors.New("malformed link")
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(payload)
	if !hmac.Equal(sig, m.Sum(nil)) {
		return "", "", errors.New("this link is not valid")
	}
	parts := strings.Split(string(payload), ".")
	if len(parts) != 3 {
		return "", "", errors.New("malformed link")
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || now.Unix() > exp {
		return "", "", errors.New("this link has expired")
	}
	return parts[0], parts[1], nil
}
