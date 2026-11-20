// Package processor consumes events, evaluates rules, folds matches into
// alerts and writes notifications to the outbox, all in one transaction per
// batch.
package processor

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/directory"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/notify"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

type Processor struct {
	Kafka     *kgo.Client // consumer-group client with auto-commit disabled
	Pool      *pgxpool.Pool
	Engine    *rules.Engine
	Directory func() *directory.Directory
	Renderer  notify.Renderer
	Publisher *alerting.Publisher
	BatchSize int
	Log       *slog.Logger
}

// Run polls batches until ctx ends. Offsets are committed only after the
// batch's database transaction commits: a crash replays the batch, and the
// unique open-fingerprint index keeps replays from creating duplicate alerts
// or notifications (occurrence counters are at-least-once).
func (p *Processor) Run(ctx context.Context) error {
	p.Log.Info("processor started")
	for {
		fetches := p.Kafka.PollRecords(ctx, p.BatchSize)
		if ctx.Err() != nil || fetches.IsClientClosed() {
			return nil
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			p.Log.Warn("fetch error", "topic", topic, "partition", partition, "err", err)
		})
		recs := fetches.Records()
		if len(recs) > 0 {
			if err := p.processWithRetry(ctx, recs); err != nil {
				return nil // only returns on shutdown
			}
			if err := p.Kafka.CommitRecords(ctx, recs...); err != nil && ctx.Err() == nil {
				p.Log.Warn("commit offsets", "err", err)
			}
		}
		p.Kafka.AllowRebalance()
	}
}

func (p *Processor) processWithRetry(ctx context.Context, recs []*kgo.Record) error {
	delay := 100 * time.Millisecond
	for {
		err := p.processBatch(ctx, recs)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p.Log.Error("process batch failed, retrying", "records", len(recs), "retryIn", delay, "err", err,
			"retryable", alerting.IsRetryable(err))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 10*time.Second {
			delay *= 2
		}
	}
}

// group collects every event in a batch that maps to the same fingerprint, so
// 500 identical errors cost one database write, not 500.
type group struct {
	rule     *rules.Compiled
	fp       string
	first    *schema.Doc
	rendered rules.Rendered
	count    int64
	firstAt  time.Time
	lastAt   time.Time
	lastID   string
	recvAt   time.Time // earliest ingest time, for latency
}

func (p *Processor) processBatch(ctx context.Context, recs []*kgo.Record) error {
	start := time.Now()
	rs := p.Engine.Current()
	groups := map[string]*group{}

	for _, rec := range recs {
		env := &event.Envelope{}
		if err := json.Unmarshal(rec.Value, env); err != nil {
			p.Log.Warn("skipping undecodable record", "partition", rec.Partition, "offset", rec.Offset, "err", err)
			continue
		}
		metrics.EventsProcessed.Inc()
		doc := schema.NewDoc(env)
		for _, c := range rs.Match(doc) {
			metrics.RuleMatches.WithLabelValues(c.Rule.ID).Inc()
			fp := c.Fingerprint(doc)
			g := groups[fp]
			if g == nil {
				g = &group{rule: c, fp: fp, first: doc, rendered: c.Render(doc), firstAt: env.ReceivedAt,
					recvAt: env.ReceivedAt}
				groups[fp] = g
			}
			g.count++
			if env.ReceivedAt.After(g.lastAt) {
				g.lastAt, g.lastID = env.ReceivedAt, env.ID
			}
			if env.ReceivedAt.Before(g.recvAt) {
				g.recvAt = env.ReceivedAt
			}
		}
	}
	if len(groups) == 0 {
		metrics.ProcessBatchDuration.Observe(time.Since(start).Seconds())
		return nil
	}

	// Stable lock order across replicas.
	ordered := make([]*group, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].fp < ordered[j].fp })

	inputs := make([]alerting.UpsertInput, len(ordered))
	for i, g := range ordered {
		inputs[i] = alerting.UpsertInput{Alert: alerting.Alert{
			ID:           event.NewID(),
			Fingerprint:  g.fp,
			RuleID:       g.rule.Rule.ID,
			Team:         g.rule.Rule.Team,
			Status:       alerting.StatusOpen,
			Severity:     g.rendered.Severity,
			Color:        g.rendered.Color,
			Title:        g.rendered.Title,
			Body:         g.rendered.Body,
			Fields:       g.rendered.Fields,
			SourceID:     g.first.Env.SourceID,
			FirstEventID: g.first.Env.ID,
			LastEventID:  g.lastID,
			Sample:       g.first.Env.Payload,
			Count:        g.count,
			FirstSeen:    g.firstAt,
			LastSeen:     g.lastAt,
		}}
	}

	var updates []alerting.Update
	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		updates = updates[:0]
		results, err := alerting.Upsert(ctx, tx, inputs)
		if err != nil {
			return err
		}
		var outbox []alerting.Notification
		dir := p.Directory()
		for i, res := range results {
			g := ordered[i]
			if !res.Inserted {
				updates = append(updates, alerting.Update{Kind: "count", Team: g.rule.Rule.Team, ID: res.ID,
					Count: res.Count, LastSeen: res.LastSeen, ReceivedAt: g.recvAt})
				continue
			}
			a := inputs[i].Alert
			a.ID, a.Count, a.LastSeen = res.ID, res.Count, res.LastSeen
			a.CreatedAt, a.UpdatedAt = time.Now(), time.Now()
			outbox = append(outbox, p.notificationsFor(&a, g, dir)...)
			alertCopy := a
			updates = append(updates, alerting.Update{Kind: "created", Team: a.Team, Alert: &alertCopy,
				ReceivedAt: g.recvAt})
		}
		return alerting.InsertNotifications(ctx, tx, outbox)
	})
	if err != nil {
		return err
	}

	for _, u := range updates {
		if u.Kind == "created" {
			metrics.AlertsCreated.WithLabelValues(u.Team).Inc()
		} else {
			metrics.AlertsDeduplicated.Inc()
		}
	}
	if err := p.Publisher.Publish(ctx, updates); err != nil {
		p.Log.Warn("publish live updates", "err", err)
	}
	metrics.ProcessBatchDuration.Observe(time.Since(start).Seconds())
	return nil
}

// notificationsFor expands the rule's routes into one outbox row per
// (person, channel). "ui" needs no row: the live feed shows every alert.
func (p *Processor) notificationsFor(a *alerting.Alert, g *group, dir *directory.Directory) []alerting.Notification {
	var out []alerting.Notification
	seen := map[string]bool{}
	for _, route := range g.rule.Rule.Routing {
		people := dir.Resolve(route.To)
		if len(people) == 0 {
			p.Log.Warn("route target resolves to nobody", "rule", a.RuleID, "to", route.To)
		}
		for _, person := range people {
			for _, ch := range route.Channels {
				if ch == "ui" {
					continue
				}
				key := person.ID + "|" + ch
				if seen[key] {
					continue
				}
				seen[key] = true
				addr := person.Address(ch)
				if addr == "" {
					p.Log.Warn("recipient has no address for channel", "person", person.ID, "channel", ch)
					continue
				}
				msg := p.Renderer.Render(ch, a)
				out = append(out, alerting.Notification{
					ID:              event.NewID(),
					AlertID:         a.ID,
					Recipient:       person.ID,
					Channel:         ch,
					Address:         addr,
					Subject:         msg.Subject,
					BodyText:        msg.BodyText,
					BodyHTML:        msg.BodyHTML,
					EventReceivedAt: g.recvAt,
				})
			}
		}
	}
	return out
}
