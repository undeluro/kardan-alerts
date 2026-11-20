// Package processor consumes events, evaluates rules, folds matches into
// alerts and writes notifications to the outbox, all in one transaction per
// batch.
package processor

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tidwall/gjson"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/directory"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/notify"
	"github.com/undeluro/kardan-alerts/internal/platform/clock"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
	"github.com/undeluro/kardan-alerts/internal/privacy"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

type Processor struct {
	Kafka     *kgo.Client // consumer-group client with auto-commit disabled
	Pool      *pgxpool.Pool
	Engine    *rules.Engine
	Directory func() *directory.Directory
	Privacy   func() *privacy.Policy
	Clock     *clock.Clock      // business time for working hours and on-call
	Fallbacks map[string]string // channel -> fallback channel, e.g. sms -> email
	Renderer  notify.Renderer
	Publisher *alerting.Publisher
	Schemas   *schema.Registry
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
	kinds    map[string]string // detected finance kind per field of the first event's schema
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
	observed := map[string]*schema.Observation{}

	for _, rec := range recs {
		env := &event.Envelope{}
		if err := json.Unmarshal(rec.Value, env); err != nil {
			p.Log.Warn("skipping undecodable record", "partition", rec.Partition, "offset", rec.Offset, "err", err)
			continue
		}
		metrics.EventsProcessed.Inc()
		doc := schema.NewDoc(env)
		sig, overrides, fields := p.Schemas.Identify(env.Payload)
		doc.Overrides = overrides
		o := observed[sig]
		if o == nil {
			o = &schema.Observation{Signature: sig, Source: env.SourceID, Format: env.Format, Sample: env}
			observed[sig] = o
		}
		if fields != nil {
			o.Fields = fields
		}
		o.Count++
		if env.ReceivedAt.After(o.LastSeen) {
			o.LastSeen = env.ReceivedAt
		}

		for _, c := range rs.Match(doc) {
			metrics.RuleMatches.WithLabelValues(c.Rule.ID).Inc()
			fp := c.Fingerprint(doc)
			g := groups[fp]
			if g == nil {
				kinds := p.Schemas.Kinds(sig)
				classify := p.classifier(doc, kinds)
				g = &group{rule: c, fp: fp, first: doc, kinds: kinds, firstAt: env.ReceivedAt, recvAt: env.ReceivedAt,
					rendered: c.RenderWith(doc.Get, func(path string) bool { return classify(path) > privacy.Public })}
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
		p.recordSchemas(ctx, observed)
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
			SampleRaw:    g.first.Env.Raw,
			SampleFormat: g.first.Env.Format,
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
		now := p.Clock.Now()
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
			outbox = append(outbox, p.notificationsFor(&a, g, dir, now)...)
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
	p.recordSchemas(ctx, observed)
	metrics.ProcessBatchDuration.Observe(time.Since(start).Seconds())
	return nil
}

// recordSchemas updates the schema catalogue. It is metadata, so failures are
// logged and never block alert processing.
func (p *Processor) recordSchemas(ctx context.Context, observed map[string]*schema.Observation) {
	if len(observed) == 0 || ctx.Err() != nil {
		return
	}
	obs := make([]*schema.Observation, 0, len(observed))
	for _, o := range observed {
		obs = append(obs, o)
	}
	created, err := p.Schemas.Record(ctx, obs)
	if err != nil {
		p.Log.Warn("record schemas", "err", err)
	}
	if len(created) == 0 {
		return
	}
	updates := make([]alerting.Update, 0, len(created))
	for _, s := range created {
		seen := map[string]bool{}
		var detected []string
		for _, f := range s.Fields {
			if f.Detected != "" && !seen[f.Detected] {
				seen[f.Detected] = true
				detected = append(detected, f.Detected)
			}
		}
		source := ""
		if len(s.Sources) > 0 {
			source = s.Sources[0]
		}
		p.Log.Info("new schema detected", "schema", s.ID, "name", s.Name, "source", source, "fields", len(s.Fields),
			"finance", detected)
		updates = append(updates, alerting.Update{Kind: "schema", Schema: &alerting.SchemaNotice{
			ID: s.ID, Name: s.Name, Source: source, Format: s.Format, Fields: len(s.Fields), Detected: detected}})
	}
	if err := p.Publisher.Publish(ctx, updates); err != nil {
		p.Log.Warn("publish schema notices", "err", err)
	}
}

type delivery struct {
	person  directory.Person
	channel string
	reason  string
}

// notificationsFor expands the rule's routes into one outbox row per
// (person, channel):
//   - targets: team members, the on-call person (rotation / follow-the-sun),
//     or a named person, resolved at the business clock's "now";
//   - channels: explicit, or "preferences" (severity + working hours of each
//     recipient, in their time zone);
//   - each message is rendered with the masking its channel requires;
//   - a fallback channel is rendered too and parked as standby, so if the
//     primary channel fails the dispatcher can switch without re-rendering.
//
// "ui" needs no row: the live feed shows every alert.
func (p *Processor) notificationsFor(a *alerting.Alert, g *group, dir *directory.Directory, now time.Time) []alerting.Notification {
	var plan []delivery
	seen := map[string]bool{}
	add := func(d delivery) {
		key := d.person.ID + "|" + d.channel
		if d.channel == "ui" || seen[key] {
			return
		}
		seen[key] = true
		plan = append(plan, d)
	}
	for _, route := range g.rule.Rule.Routing {
		people, why := dir.Resolve(route.To, now)
		if len(people) == 0 {
			p.Log.Warn("route target resolves to nobody", "rule", a.RuleID, "to", route.To)
		}
		for _, person := range people {
			for _, ch := range route.Channels {
				if ch != "preferences" {
					add(delivery{person, ch, join(why, "rule routes "+ch)})
					continue
				}
				chs, prefWhy := dir.ChannelsFor(person, a.Team, a.Severity, now)
				for _, c := range chs {
					add(delivery{person, c, join(why, prefWhy)})
				}
			}
		}
	}

	pol := p.Privacy()
	classify := p.classifier(g.first, g.kinds)
	masked := map[privacy.Level]*alerting.Alert{}
	render := func(channel string) notify.Message {
		lvl := pol.ChannelLevel(channel)
		ma, ok := masked[lvl]
		if !ok {
			get := func(path string) gjson.Result {
				r := g.first.Get(path)
				if r.Exists() && classify(path) > lvl {
					m := privacy.Mask(r.String())
					return gjson.Result{Type: gjson.String, Str: m, Raw: strconv.Quote(m)}
				}
				return r
			}
			pres := g.rule.RenderWith(get, nil)
			cp := *a
			cp.Title, cp.Body, cp.Fields = pres.Title, pres.Body, pres.Fields
			ma = &cp
			masked[lvl] = ma
		}
		return p.Renderer.Render(channel, ma)
	}

	var out []alerting.Notification
	for _, d := range plan {
		addr := d.person.Address(d.channel)
		if addr == "" {
			p.Log.Warn("recipient has no address for channel", "person", d.person.ID, "channel", d.channel)
			continue
		}
		msg := render(d.channel)
		primary := alerting.Notification{
			ID: event.NewID(), AlertID: a.ID, Recipient: d.person.ID, Channel: d.channel, Address: addr,
			Subject: msg.Subject, BodyText: msg.BodyText, BodyHTML: msg.BodyHTML, EventReceivedAt: g.recvAt,
			Reason: d.reason,
		}
		out = append(out, primary)

		fb := p.Fallbacks[d.channel]
		if fb == "" || seen[d.person.ID+"|"+fb] {
			continue // no fallback, or the person already gets that channel
		}
		if fbAddr := d.person.Address(fb); fbAddr != "" {
			fm := render(fb)
			out = append(out, alerting.Notification{
				ID: event.NewID(), AlertID: a.ID, Recipient: d.person.ID, Channel: fb, Address: fbAddr,
				Subject: fm.Subject, BodyText: fm.BodyText, BodyHTML: fm.BodyHTML, EventReceivedAt: g.recvAt,
				Status: "standby", FallbackFor: primary.ID, Reason: "fallback if " + d.channel + " fails",
			})
		}
	}
	return out
}

// classifier returns the privacy level of a field for this event, using the
// facet mapping (so $account resolves to the real path) and the finance kinds
// detected for its schema.
func (p *Processor) classifier(doc *schema.Doc, kinds map[string]string) func(path string) privacy.Level {
	pol := p.Privacy()
	return func(path string) privacy.Level {
		resolved := path
		if strings.HasPrefix(path, "$") {
			if o, ok := doc.Overrides[path]; ok && o != "" {
				resolved = o
			}
		}
		return pol.LevelOf(resolved, kinds[resolved], doc.Get(path))
	}
}

func join(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}
