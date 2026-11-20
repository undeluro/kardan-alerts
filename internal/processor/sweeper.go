package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

// RunSweeper does the time-driven work: closing alerts that went silent,
// closing digest windows (and sending their digest), sending throttling
// digests, and closing incidents. Every processor replica runs it, but a
// short Redis lease lets one replica sweep per tick; every step is also safe
// to run twice (conditional updates and atomic claims).
func (p *Processor) RunSweeper(ctx context.Context, rdb *redis.Client, instance string, every time.Duration) error {
	if every <= 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		ok, err := rdb.SetNX(ctx, "kardan:sweeper", instance, every-500*time.Millisecond).Result()
		if err != nil || !ok {
			continue
		}
		p.sweep(ctx)
	}
}

func (p *Processor) sweep(ctx context.Context) {
	for _, step := range []struct {
		name string
		fn   func(context.Context) error
	}{
		{"silent alerts", p.sweepSilent},
		{"digest windows", p.sweepWindows},
		{"throttling digests", p.sweepHeld},
		{"incidents", p.sweepIncidents},
	} {
		if err := step.fn(ctx); err != nil && ctx.Err() == nil {
			p.Log.Warn("sweep step failed", "step", step.name, "err", err)
		}
	}
}

// sweepSilent resolves alerts whose rule says autoResolve.after and that saw
// no new matching event for that long.
func (p *Processor) sweepSilent(ctx context.Context) error {
	for _, c := range p.Engine.Current().Rules {
		if c.ResolveAfter <= 0 {
			continue
		}
		reason := fmt.Sprintf("no new events for %s", c.ResolveAfter)
		var updates []alerting.Update
		err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
			updates = updates[:0]
			resolved, err := alerting.ResolveSilent(ctx, tx, c.Rule.ID, c.ResolveAfter, "auto: "+reason)
			if err != nil {
				return err
			}
			var outbox []alerting.Notification
			for _, a := range resolved {
				clear, err := p.allClear(ctx, tx, a, reason)
				if err != nil {
					return err
				}
				outbox = append(outbox, clear...)
				a.Sample, a.SampleRaw = nil, ""
				updates = append(updates, alerting.Update{Kind: "status", Team: a.Team, Alert: a})
			}
			return alerting.InsertNotifications(ctx, tx, outbox)
		})
		if err != nil {
			return err
		}
		if len(updates) > 0 {
			metrics.AlertsAutoResolved.WithLabelValues("silence").Add(float64(len(updates)))
			p.Log.Info("alerts auto-resolved after silence", "rule", c.Rule.ID, "count", len(updates))
			_ = p.Publisher.Publish(ctx, updates)
		}
	}
	return nil
}

// sweepWindows closes grouped alerts whose window ended: one digest per
// window goes to the rule's recipients, and the window's alert resolves.
func (p *Processor) sweepWindows(ctx context.Context) error {
	due, err := alerting.DueWindows(ctx, p.Pool, 200)
	if err != nil || len(due) == 0 {
		return err
	}
	rs := p.Engine.Current()
	dir := p.Directory()
	now := p.Clock.Now()
	var updates []alerting.Update
	for _, a := range due {
		label := windowLabel(a.WindowStart, a.WindowEnd)
		occurrences := "1 event"
		if a.Count != 1 {
			occurrences = fmt.Sprintf("%d events", a.Count)
		}
		reason := fmt.Sprintf("auto: window %s closed (%s)", label, occurrences)
		var resolved *alerting.Alert
		err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
			r, err := alerting.ResolveByID(ctx, tx, a.ID, reason)
			if alerting.IsNotFound(err) {
				resolved = nil
				return nil // someone resolved it already
			}
			if err != nil {
				return err
			}
			resolved = r
			c := rs.ByID(a.RuleID)
			if c == nil {
				return nil // rule deleted since: close silently
			}
			g := p.groupFromAlert(c, r)
			g.kind, g.digest = "digest", occurrences+" in "+label
			return alerting.InsertNotifications(ctx, tx, p.notificationsFor(ctx, r, g, dir, now))
		})
		if err != nil {
			return err
		}
		if resolved != nil {
			metrics.AlertsAutoResolved.WithLabelValues("window").Inc()
			metrics.DigestsSent.WithLabelValues("window").Inc()
			resolved.Sample, resolved.SampleRaw = nil, ""
			updates = append(updates, alerting.Update{Kind: "status", Team: resolved.Team, Alert: resolved})
		}
	}
	if len(updates) > 0 {
		p.Log.Info("digest windows closed", "count", len(updates))
		_ = p.Publisher.Publish(ctx, updates)
	}
	return nil
}

// groupFromAlert rebuilds what notificationsFor needs from a stored alert:
// its first event (for masking) and its schema's detected field kinds.
func (p *Processor) groupFromAlert(c *rules.Compiled, a *alerting.Alert) *group {
	env := &event.Envelope{ID: a.FirstEventID, SourceID: a.SourceID, ReceivedAt: a.FirstSeen, Payload: a.Sample,
		Raw: a.SampleRaw, Format: a.SampleFormat}
	doc := schema.NewDoc(env)
	doc.FX = p.FX()
	sig, overrides, _ := p.Schemas.Identify(a.Sample)
	doc.Overrides = overrides
	return &group{rule: c, fp: a.Fingerprint, first: doc, kinds: p.Schemas.Kinds(sig), recvAt: time.Now()}
}

// sweepHeld turns throttled messages into one digest per person and channel.
func (p *Processor) sweepHeld(ctx context.Context) error {
	thr := p.Directory().Throttling()
	n := 0
	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		n = 0
		groups, err := alerting.ClaimHeld(ctx, tx)
		if err != nil || len(groups) == 0 {
			return err
		}
		var outbox []alerting.Notification
		for _, hg := range groups {
			window := 10 * time.Minute
			if l, ok := thr.Channels[hg.Channel]; ok {
				window = l.Duration()
			}
			msg := p.Renderer.RenderDigest(hg.Channel, hg.Subjects, window)
			outbox = append(outbox, alerting.Notification{
				ID: event.NewID(), AlertID: hg.AlertIDs[len(hg.AlertIDs)-1], Recipient: hg.Recipient,
				Channel: hg.Channel, Address: hg.Address, Subject: msg.Subject, BodyText: msg.BodyText,
				BodyHTML: msg.BodyHTML, EventReceivedAt: time.Now(), Kind: "digest",
				Reason: fmt.Sprintf("throttling digest of %d held messages", len(hg.IDs)),
			})
		}
		n = len(outbox)
		return alerting.InsertNotifications(ctx, tx, outbox)
	})
	if n > 0 {
		metrics.DigestsSent.WithLabelValues("throttle").Add(float64(n))
		p.Log.Info("throttling digests sent", "count", n)
	}
	return err
}

func (p *Processor) sweepIncidents(ctx context.Context) error {
	n, err := alerting.CloseIncidents(ctx, p.Pool, p.Correlation().WindowDuration())
	if n > 0 {
		p.Log.Info("incidents closed", "count", n)
	}
	return err
}
