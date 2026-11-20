package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tidwall/gjson"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/finance"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

// sweepAbsence opens "expected event missing" alerts.
//
//	deadline:  on business days of the rule's calendar, after HH:MM local, for
//	           every expected sender with nothing since local midnight;
//	heartbeat: for every sender (expected, or seen in the last 24 h) silent for
//	           longer than `every`, only while the calendar (if any) is open.
func (p *Processor) sweepAbsence(ctx context.Context) error {
	if p.Redis == nil {
		return nil
	}
	now := p.Clock.Now() // business time: lets a demo jump past the deadline
	real := time.Now()
	for _, c := range p.Engine.Current().OfKind(rules.KindAbsence) {
		spec := c.Absence()
		cal, hasCal := p.cals().Get(spec.Calendar)
		lastSeen, err := p.Redis.HGetAll(ctx, absenceKey(c.Rule.ID)).Result()
		if err != nil {
			return err
		}
		keys := map[string]bool{}
		for _, k := range spec.Expect {
			keys[k] = true
		}
		if spec.By == "" {
			keys[noSender] = true
		}
		if spec.Deadline < 0 {
			// Heartbeats also watch every sender seen in the last day.
			for k, v := range lastSeen {
				if r, _ := parseSeen(v); real.Sub(r) < 24*time.Hour {
					keys[k] = true
				}
			}
		}
		for key := range keys {
			lastReal, lastBiz := parseSeen(lastSeen[key])
			var fp, why string
			vars := map[string]gjson.Result{"$key": schema.Str(displayKey(key))}
			if spec.Deadline >= 0 {
				if !hasCal || !cal.IsBusinessDay(now) {
					continue // weekend or holiday: nothing is expected
				}
				deadline := cal.At(now, spec.Deadline)
				if now.Before(deadline) || !lastBiz.Before(cal.DayStart(now)) {
					continue // not due yet, or it arrived today
				}
				day := now.In(cal.Location()).Format("2006-01-02")
				fp = c.AbsenceFingerprint(key, day)
				why = fmt.Sprintf("Not received by %s %s on business day %s", deadline.Format("15:04"), cal.Location(), day)
				vars["$deadline"] = schema.Str(deadline.Format("15:04 MST"))
				vars["$lastSeen"] = schema.Str(describeLast(lastBiz, now))
			} else {
				if hasCal && !cal.IsOpen(now) {
					continue // market closed: silence is expected
				}
				silent := real.Sub(lastReal)
				if !lastReal.IsZero() && silent < spec.Every {
					continue
				}
				fp = c.AbsenceFingerprint(key, "")
				why = fmt.Sprintf("Nothing for more than %s", shortDur(spec.Every))
				vars["$expectedEvery"] = schema.Str(shortDur(spec.Every))
				vars["$silentFor"] = schema.Str(shortDur(silent))
				// Shown in business time, like the rest of the UI.
				vars["$lastSeen"] = schema.Str(describeLast(lastBiz, lastBiz.Add(silent)))
			}
			vars["$reason"] = schema.Str(why)
			if err := p.openSynthetic(ctx, c, fp, key, vars, why); err != nil {
				return err
			}
		}
	}
	return nil
}

func displayKey(key string) string {
	if key == noSender {
		return ""
	}
	return key
}

func describeLast(last, now time.Time) string {
	if last.IsZero() {
		return "never"
	}
	return fmt.Sprintf("%s (%s ago)", last.UTC().Format("Mon 15:04:05 UTC"), shortDur(now.Sub(last)))
}

// shortDur: "45 s", "12 min", "2 h 5 min", "3 days".
func shortDur(d time.Duration) string {
	switch {
	case d < 0:
		return "0 s"
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Round(time.Second)/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Round(time.Minute)/time.Minute))
	case d < 48*time.Hour:
		h, m := int(d/time.Hour), int((d%time.Hour).Round(time.Minute)/time.Minute)
		if m == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %d min", h, m)
	default:
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	}
}

// openSynthetic opens an alert that no single event caused (absence). The
// payload describes what was expected, so templates and runbooks can use it.
func (p *Processor) openSynthetic(ctx context.Context, c *rules.Compiled, fp, key string, vars map[string]gjson.Result, why string) error {
	var exists bool
	if err := p.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM alerts WHERE fingerprint = $1 AND status <> 'resolved')`,
		fp).Scan(&exists); err != nil || exists {
		return err
	}
	// Mark it open first: if the expected event races in, the processor
	// resolves the alert (worst case it resolves one that never opened).
	if err := p.Redis.SAdd(ctx, absenceOpenKey(c.Rule.ID), fp).Err(); err != nil {
		return err
	}
	spec := c.Absence()
	payload := map[string]any{"type": "EXPECTED_EVENT_MISSING", "rule": c.Rule.ID, "reason": why, "severity": "error"}
	if spec.By != "" {
		payload[spec.By] = displayKey(key)
	}
	if spec.Calendar != "" {
		payload["calendar"] = spec.Calendar
	}
	raw, _ := json.Marshal(payload)
	env := &event.Envelope{ID: event.NewID(), SourceID: "kardan.absence", ReceivedAt: time.Now().UTC(), Payload: raw, Format: "json"}
	doc := schema.NewDoc(env).WithVars(vars)
	doc.FX = p.FX()
	sig, overrides, _ := p.Schemas.Identify(raw)
	doc.Overrides = overrides
	g := p.newGroup(c, fp, doc, sig, p.Correlation())
	g.count = 1
	g.lastAt, g.lastID = env.ReceivedAt, env.ID
	a := alerting.Alert{ID: event.NewID(), Fingerprint: fp, RuleID: c.Rule.ID, Team: c.Rule.Team,
		Status: alerting.StatusOpen, Severity: g.rendered.Severity, Color: g.rendered.Color, Title: g.rendered.Title,
		Body: g.rendered.Body, Fields: g.rendered.Fields, SourceID: env.SourceID, FirstEventID: env.ID,
		LastEventID: env.ID, Sample: raw, SampleFormat: "json", Count: 1, FirstSeen: env.ReceivedAt,
		LastSeen: env.ReceivedAt, Impact: g.impact, BaseImpact: g.baseImpact, CutoffName: g.cutoffName, CutoffAt: g.cutoffAt,
		Vars: schema.EncodeVars(vars)}
	var created *alerting.Alert
	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		res, err := alerting.Upsert(ctx, tx, []alerting.UpsertInput{{Alert: a}})
		if err != nil || !res[0].Inserted {
			return err
		}
		a.ID = res[0].ID
		created = &a
		if err := p.startRunbooks(ctx, tx, &a); err != nil {
			return err
		}
		return alerting.InsertNotifications(ctx, tx, p.notificationsFor(ctx, &a, g, p.Directory(), p.Clock.Now()))
	})
	if err != nil || created == nil {
		return err
	}
	metrics.AlertsCreated.WithLabelValues(created.Team).Inc()
	p.Log.Info("expected event missing", "rule", c.Rule.ID, "sender", displayKey(key), "why", why)
	created.Sample = nil
	return p.Publisher.Publish(ctx, []alerting.Update{{Kind: "created", Team: created.Team, Alert: created, ReceivedAt: env.ReceivedAt}})
}

// sweepAggregates resolves aggregate alerts whose window total dropped back
// below the threshold.
func (p *Processor) sweepAggregates(ctx context.Context) error {
	if p.Redis == nil {
		return nil
	}
	for _, c := range p.Engine.Current().OfKind(rules.KindAggregate) {
		open, err := alerting.OpenByRule(ctx, p.Pool, c.Rule.ID)
		if err != nil {
			return err
		}
		spec := c.Aggregate()
		for _, a := range open {
			total, err := peekWindow(ctx, p.Redis, c.Rule.ID, a.Fingerprint, spec.Window)
			if err != nil {
				return err
			}
			value := float64(total.Count)
			if spec.SumField != "" {
				value = total.Sum
			}
			if value >= spec.Threshold {
				continue
			}
			reason := fmt.Sprintf("back below threshold (%s in the last %s)", fmtNum(value), rules.HumanWindow(spec.Window))
			if err := p.resolveWithAllClear(ctx, a, reason); err != nil {
				return err
			}
			metrics.AlertsAutoResolved.WithLabelValues("aggregate").Inc()
		}
	}
	return nil
}

func fmtNum(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', 2, 64)
}

func (p *Processor) resolveWithAllClear(ctx context.Context, a *alerting.Alert, reason string) error {
	var resolved *alerting.Alert
	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		r, err := alerting.ResolveByID(ctx, tx, a.ID, "auto: "+reason)
		if alerting.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		resolved = r
		clear, err := p.allClear(ctx, tx, r, reason)
		if err != nil {
			return err
		}
		return alerting.InsertNotifications(ctx, tx, clear)
	})
	if err != nil || resolved == nil {
		return err
	}
	resolved.Sample, resolved.SampleRaw = nil, ""
	return p.Publisher.Publish(ctx, []alerting.Update{{Kind: "status", Team: resolved.Team, Alert: resolved}})
}

// sweepUrgency raises impact as cut-offs approach (and keeps it high once
// missed), so the feed re-orders itself as deadlines near.
func (p *Processor) sweepUrgency(ctx context.Context) error {
	open, err := alerting.OpenWithCutoff(ctx, p.Pool)
	if err != nil || len(open) == 0 {
		return err
	}
	now := p.Clock.Now()
	var ids []string
	var impacts []int
	var updates []alerting.Update
	for _, a := range open {
		want := min(100, a.BaseImpact+finance.Urgency(*a.CutoffAt, now))
		if want == a.Impact {
			continue
		}
		ids, impacts = append(ids, a.ID), append(impacts, want)
		a.Impact, a.Sample, a.SampleRaw = want, nil, ""
		updates = append(updates, alerting.Update{Kind: "status", Team: a.Team, Alert: a})
	}
	if err := alerting.SetImpacts(ctx, p.Pool, ids, impacts); err != nil {
		return err
	}
	if len(updates) > 0 {
		_ = p.Publisher.Publish(ctx, updates)
	}
	return nil
}

// sweepEscalations takes the next escalation step for unacknowledged
// alerts. Each step's delay shrinks as the alert's cut-off approaches: with
// less than an hour left, delays scale by the time remaining (to 20% at most).
func (p *Processor) sweepEscalations(ctx context.Context) error {
	businessNow, real := p.Clock.Now(), time.Now()
	dir := p.Directory()
	for _, c := range p.Engine.Current().Rules {
		steps := c.EscalationSteps()
		if len(steps) == 0 || !c.Rule.IsEnabled() {
			continue
		}
		pending, err := alerting.Unacknowledged(ctx, p.Pool, c.Rule.ID, len(steps))
		if err != nil {
			return err
		}
		for _, a := range pending {
			step := steps[a.Escalation]
			after, note := tightened(step.After, a.CutoffAt, businessNow)
			if note != "" {
				note = fmt.Sprintf(" (normally %s; %s %s)", shortDur(step.After), note, deref2(a.CutoffName))
			}
			due := a.CreatedAt.Add(after)
			if real.Before(due) {
				continue
			}
			level := a.Escalation
			var updated *alerting.Alert
			err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
				ok, err := alerting.MarkEscalated(ctx, tx, a.ID, level)
				if err != nil || !ok {
					return err
				}
				g := p.groupFromAlert(c, a)
				g.kind, g.routes = "escalation", []rules.Route{step.Route}
				g.reasonPrefix = fmt.Sprintf("escalation %d/%d: not acknowledged within %s%s", level+1, len(steps),
					shortDur(after), note)
				a.Escalation = level + 1
				updated = a
				return alerting.InsertNotifications(ctx, tx, p.notificationsFor(ctx, a, g, dir, businessNow))
			})
			if err != nil {
				return err
			}
			if updated != nil {
				p.Log.Warn("alert escalated", "alert", a.ID, "rule", c.Rule.ID, "level", level+1, "to", step.Route.To)
				metrics.Escalations.WithLabelValues(c.Rule.Team).Inc()
				updated.Sample, updated.SampleRaw = nil, ""
				_ = p.Publisher.Publish(ctx, []alerting.Update{{Kind: "status", Team: updated.Team, Alert: updated}})
			}
		}
	}
	return nil
}

// tightened shortens an escalation delay as the alert's cut-off nears: with
// less than an hour left the delay scales with the time remaining, down to
// 20% of the configured delay once the cut-off is minutes away or missed.
func tightened(after time.Duration, cutoff *time.Time, now time.Time) (time.Duration, string) {
	if cutoff == nil {
		return after, ""
	}
	left := cutoff.Sub(now)
	if left >= time.Hour {
		return after, ""
	}
	f := max(0.2, float64(left)/float64(time.Hour))
	note := shortDur(left) + " left to"
	if left <= 0 {
		note = "missed"
	}
	return time.Duration(float64(after) * f), note
}

func deref2(s *string) string {
	if s == nil {
		return "cut-off"
	}
	return *s
}
