package processor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tidwall/gjson"

	"github.com/undeluro/kardan-alerts/internal/actions"
	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/audit"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/notify"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/runbooks"
	"github.com/undeluro/kardan-alerts/internal/runner"
)

// ---- runbooks in messages --------------------------------------------------------

// attachedRunbooks lists the runbooks for an alert being notified.
func (p *Processor) attachedRunbooks(a *alerting.Alert, g *group) []*runbooks.Runbook {
	if p.Runner == nil || g.first == nil {
		return nil
	}
	sig := ""
	if p.Schemas != nil && len(g.first.Env.Payload) > 0 {
		sig, _, _ = p.Schemas.Identify(g.first.Env.Payload)
	}
	return p.Runner.For(a, g.first, sig)
}

// runbookSummaries fills the steps with the channel's (masked) values.
func (p *Processor) runbookSummaries(list []*runbooks.Runbook, a *alerting.Alert, get func(string) gjson.Result) []notify.RunbookSummary {
	if len(list) == 0 {
		return nil
	}
	reg := p.Actions()
	vars := runbooks.AlertVars(a.ID, a.Title, a.Team, a.Severity, string(a.Status), alerting.ShortCode(a.ID))
	resolve := func(path string) gjson.Result {
		if v, ok := vars[path]; ok {
			return v
		}
		return get(path)
	}
	out := make([]notify.RunbookSummary, 0, len(list))
	for _, rb := range list {
		s := notify.RunbookSummary{Name: rb.Name, URL: rb.URL}
		for _, f := range rb.Fill(resolve) {
			text := f.Text
			if text == "" && f.Action != "" {
				text = reg.Label(f.Action, f.Params)
			}
			s.Steps = append(s.Steps, notify.RunbookLine{Text: text, If: f.If, Action: f.Action != "",
				Approval: f.Action != "" && reg.Approval(f.Action, f.Approval) == "required"})
		}
		out = append(out, s)
	}
	return out
}

// startRunbooks begins auto runbooks for a new alert, in its transaction.
func (p *Processor) startRunbooks(ctx context.Context, tx pgx.Tx, a *alerting.Alert) error {
	if p.Runner == nil {
		return nil
	}
	doc, sig := p.Runner.Doc(a)
	for _, rb := range p.Runner.For(a, doc, sig) {
		if !rb.Auto {
			continue
		}
		if err := p.Runner.Advance(ctx, tx, a, rb, 0, ""); err != nil {
			return fmt.Errorf("start runbook %s: %w", rb.ID, err)
		}
		p.Log.Info("runbook started", "alert", a.ID, "runbook", rb.ID)
	}
	return nil
}

// ---- executor ------------------------------------------------------------------

// RunActions executes queued runbook steps until ctx ends. Any number of
// workers can run it: steps are claimed with SKIP LOCKED and a lease, and the
// target receives the run id as an Idempotency-Key.
func (p *Processor) RunActions(ctx context.Context) error {
	if p.Runner == nil {
		return nil
	}
	p.Log.Info("runbook executor started")
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for n := 0; ; n++ {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		if n%5 == 0 {
			p.cancelForResolved(ctx)
		}
		p.notifyApprovers(ctx)
		runs, err := alerting.ClaimRuns(ctx, p.Pool, 8, time.Minute)
		if err != nil {
			if ctx.Err() == nil {
				p.Log.Warn("claim runbook steps", "err", err)
			}
			continue
		}
		var wg sync.WaitGroup
		for _, run := range runs {
			wg.Add(1)
			go func(run *alerting.Run) {
				defer wg.Done()
				if err := p.execute(ctx, run); err != nil && ctx.Err() == nil {
					p.Log.Error("runbook step", "run", run.ID, "err", err)
				}
			}(run)
		}
		wg.Wait()
	}
}

func (p *Processor) execute(ctx context.Context, run *alerting.Run) error {
	store := &alerting.Store{Pool: p.Pool}
	a, err := store.Get(ctx, run.AlertID)
	if err != nil {
		return err
	}
	var res actions.Result
	start := time.Now()
	switch {
	case a.Status == alerting.StatusResolved:
		res = actions.Result{Output: "not run: the alert was resolved"}
	case run.Action == "wait":
		res = actions.Result{OK: true, Output: "waited " + run.Params["duration"]}
	case run.Action == "escalate":
		res = p.escalate(ctx, a, run)
	default:
		// The idempotency key is per attempt: a deliberate retry must reach the
		// target again, while a duplicate of the same attempt is answered once.
		res = p.Actions().Execute(ctx, actions.Call{RunID: fmt.Sprintf("%s-%d", run.ID, run.Attempts), Action: run.Action, Params: run.Params, AlertID: a.ID,
			AlertTitle: a.Title, Team: a.Team, RequestedBy: run.RequestedBy, ApprovedBy: str(run.ApprovedBy)})
	}
	metrics.RunbookActions.WithLabelValues(run.Action, okLabel(res.OK)).Inc()
	metrics.RunbookActionDuration.WithLabelValues(run.Action).Observe(time.Since(start).Seconds())

	status := alerting.RunSucceeded
	var retryAt *time.Time
	if !res.OK {
		status = alerting.RunFailed
		if a.Status != alerting.StatusResolved && run.Attempts < run.MaxAttempts {
			t := time.Now().Add(time.Duration(run.Attempts) * 5 * time.Second)
			retryAt = &t
		}
	}
	if a.Status == alerting.StatusResolved {
		status = alerting.RunSkipped
	}
	output := res.Output
	if run.MaxAttempts > 1 {
		output = fmt.Sprintf("attempt %d/%d: %s", run.Attempts, run.MaxAttempts, res.Output)
	}
	var resolved *alerting.Alert
	err = pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		resolved = nil
		done, err := alerting.FinishRun(ctx, tx, run.ID, status, output, retryAt)
		if err != nil {
			return err
		}
		what := map[string]string{alerting.RunSucceeded: "action.succeeded", alerting.RunFailed: "action.failed",
			alerting.RunSkipped: "action.skipped"}[status]
		if retryAt != nil {
			what = "action.retry_scheduled"
		}
		if _, err := audit.Append(ctx, tx, audit.Event{Actor: runner.System, Action: what, Subject: "run:" + run.ID,
			Detail: map[string]any{"alert": a.ID, "runbook": run.RunbookID, "step": run.Step + 1, "action": run.Action,
				"label": run.Label, "attempt": run.Attempts, "output": res.Output, "requestedBy": run.RequestedBy,
				"approvedBy": str(run.ApprovedBy)}}); err != nil {
			return err
		}
		if retryAt != nil || status == alerting.RunSkipped {
			return nil
		}
		rb, ok := p.Runner.Catalog.Current().Get(run.RunbookID)
		if !ok {
			return nil // the runbook was deleted meanwhile
		}
		if status == alerting.RunSucceeded {
			if run.Step < len(rb.Steps) && rb.Steps[run.Step].Then == "resolve" {
				r, err := p.resolveByRunbook(ctx, tx, a, done)
				if err != nil {
					return err
				}
				resolved = r
				return nil
			}
			if done.RepeatEvery > 0 && done.Approval == "none" {
				next := *done
				next.ID, next.Status, next.Output, next.Attempts = event.NewID(), alerting.RunQueued, "", 0
				next.NotBefore = time.Now().Add(time.Duration(done.RepeatEvery) * time.Millisecond)
				if err := alerting.InsertRun(ctx, tx, &next); err != nil {
					return err
				}
			}
		}
		return p.Runner.Advance(ctx, tx, a, rb, run.Step+1, status)
	})
	if err != nil {
		return err
	}
	p.Log.Info("runbook step finished", "alert", a.ID, "runbook", run.RunbookID, "step", run.Step+1,
		"action", run.Action, "status", status, "retry", retryAt != nil, "output", res.Output)
	if resolved != nil {
		resolved.Sample, resolved.SampleRaw = nil, ""
		_ = p.Publisher.Publish(ctx, []alerting.Update{{Kind: "status", Team: resolved.Team, Alert: resolved}})
		return nil
	}
	p.publishRunbookChange(ctx, a.ID)
	return nil
}

func okLabel(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

// resolveByRunbook closes the alert after a step marked "then: resolve" worked.
func (p *Processor) resolveByRunbook(ctx context.Context, tx pgx.Tx, a *alerting.Alert, run *alerting.Run) (*alerting.Alert, error) {
	reason := fmt.Sprintf("runbook %s step %d succeeded (%s)", run.RunbookID, run.Step+1, run.Label)
	r, err := alerting.ResolveByID(ctx, tx, a.ID, "auto: "+reason)
	if alerting.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := alerting.CancelOpenRuns(ctx, tx, a.ID, "alert resolved by the runbook"); err != nil {
		return nil, err
	}
	if _, err := audit.Append(ctx, tx, audit.Event{Actor: runner.System, Action: "alert.resolve", Subject: "alert:" + a.ID,
		Detail: map[string]any{"reason": reason, "run": run.ID}}); err != nil {
		return nil, err
	}
	clear, err := p.allClear(ctx, tx, r, reason)
	if err != nil {
		return nil, err
	}
	metrics.AlertsAutoResolved.WithLabelValues("runbook").Inc()
	return r, alerting.InsertNotifications(ctx, tx, clear)
}

// escalate notifies another team, person or on-call (the built-in action).
func (p *Processor) escalate(ctx context.Context, a *alerting.Alert, run *alerting.Run) actions.Result {
	to := run.Params["to"]
	if to == "" && run.Params["team"] != "" {
		to = "team:" + run.Params["team"]
	}
	if !strings.Contains(to, ":") {
		to = "team:" + to
	}
	dir := p.Directory()
	if !dir.Exists(to) {
		return actions.Result{Output: fmt.Sprintf("%s is not a known team, person or on-call rota", to)}
	}
	c := p.Engine.Current().ByID(a.RuleID)
	if c == nil {
		return actions.Result{Output: "the alert's rule no longer exists, so the message cannot be rendered"}
	}
	channels := []string{"sms", "email"}
	if ch := strings.TrimSpace(run.Params["channels"]); ch != "" {
		channels = strings.Split(strings.ReplaceAll(ch, " ", ""), ",")
	}
	g := p.groupFromAlert(c, a)
	g.kind, g.routes = "escalation", []rules.Route{{To: to, Channels: channels}}
	by := "requested by " + run.RequestedBy
	if run.ApprovedBy != nil {
		by += ", approved by " + *run.ApprovedBy
	}
	g.reasonPrefix = fmt.Sprintf("runbook %s step %d: escalate (%s)", run.RunbookID, run.Step+1, by)
	notes := p.notificationsFor(ctx, a, g, dir, p.Clock.Now())
	if len(notes) == 0 {
		return actions.Result{Output: to + " resolves to nobody with an address for " + strings.Join(channels, ", ")}
	}
	if err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error { return alerting.InsertNotifications(ctx, tx, notes) }); err != nil {
		return actions.Result{Output: err.Error()}
	}
	var who []string
	for _, n := range notes {
		if n.Status != "standby" {
			who = append(who, n.Recipient+" ("+n.Channel+")")
		}
	}
	metrics.Escalations.WithLabelValues(a.Team).Inc()
	return actions.Result{OK: true, Output: "escalated to " + to + ": " + strings.Join(who, ", ")}
}

// ---- approvals -----------------------------------------------------------------

// notifyApprovers tells eligible people that a step waits for them: the
// alert's team and its on-call, minus the requester (four-eyes). SMS carries
// the APPROVE code, email signed one-click links.
func (p *Processor) notifyApprovers(ctx context.Context) {
	runs, err := alerting.AwaitingApproval(ctx, p.Pool, true)
	if err != nil || len(runs) == 0 {
		return
	}
	store := &alerting.Store{Pool: p.Pool}
	dir := p.Directory()
	now := p.Clock.Now()
	for _, run := range runs {
		a, err := store.Get(ctx, run.AlertID)
		if err != nil {
			continue
		}
		requester, hasRequester := dir.Find(run.RequestedBy)
		seen := map[string]bool{}
		var notes []alerting.Notification
		for _, target := range []string{"oncall:" + a.Team, "team:" + a.Team} {
			people, why := dir.Resolve(target, now)
			for _, person := range people {
				if seen[person.ID] || (hasRequester && person.ID == requester.ID) {
					continue
				}
				seen[person.ID] = true
				ap := notify.Approval{Label: run.Label, AlertTitle: a.Title, AlertID: a.ID, Risk: run.Risk,
					RequestedBy: run.RequestedBy, Code: run.Code}
				exp := time.Now().Add(24 * time.Hour)
				ap.ApproveURL = fmt.Sprintf("%s/approve/%s", p.Renderer.PublicURL, runner.SignLink(p.ApprovalSecret, run.ID, person.ID, exp))
				ap.RejectURL = ap.ApproveURL + "?decision=reject"
				reason := fmt.Sprintf("approver for a %s-risk step (%s; not the requester)", run.Risk, why)
				for _, ch := range []string{"sms", "email"} {
					addr := person.Address(ch)
					if addr == "" {
						continue
					}
					msg := p.Renderer.RenderApproval(ch, ap)
					notes = append(notes, alerting.Notification{ID: event.NewID(), AlertID: a.ID, Recipient: person.ID,
						Channel: ch, Address: addr, Subject: msg.Subject, BodyText: msg.BodyText, BodyHTML: msg.BodyHTML,
						EventReceivedAt: run.CreatedAt, Reason: reason, Kind: "approval"})
				}
			}
		}
		err = pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
			ok, err := alerting.MarkNotified(ctx, tx, run.ID)
			if err != nil || !ok {
				return err
			}
			return alerting.InsertNotifications(ctx, tx, notes)
		})
		if err != nil {
			p.Log.Warn("notify approvers", "run", run.ID, "err", err)
			continue
		}
		p.Log.Info("approval requested", "run", run.ID, "code", run.Code, "label", run.Label, "approvers", len(seen))
	}
}

// cancelForResolved stops pending steps of alerts that were resolved.
func (p *Processor) cancelForResolved(ctx context.Context) {
	_, err := p.Pool.Exec(ctx, `UPDATE action_runs r SET status = 'skipped', reason = 'alert resolved',
		updated_at = now(), finished_at = now()
		FROM alerts a WHERE a.id = r.alert_id AND a.status = 'resolved' AND r.status IN ('queued', 'awaiting_approval')`)
	if err != nil && !errors.Is(err, context.Canceled) {
		p.Log.Warn("cancel steps of resolved alerts", "err", err)
	}
}

// publishRunbookChange pushes the alert (with its pending approvals) to the feed.
func (p *Processor) publishRunbookChange(ctx context.Context, alertID string) {
	store := &alerting.Store{Pool: p.Pool}
	a, err := store.Get(ctx, alertID)
	if err != nil {
		return
	}
	if n, err := alerting.PendingApprovals(ctx, p.Pool, []string{a.ID}); err == nil {
		a.PendingApprovals = n[a.ID]
	}
	a.Sample, a.SampleRaw = nil, ""
	_ = p.Publisher.Publish(ctx, []alerting.Update{{Kind: "status", Team: a.Team, Alert: a}})
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
