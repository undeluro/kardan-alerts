// Package processor consumes events, evaluates rules, folds matches into
// alerts, links them into incidents and writes notifications to the outbox,
// all in one transaction per batch. A sweeper closes windows, silent alerts
// and incidents, and sends digests.
package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/tidwall/gjson"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/undeluro/kardan-alerts/internal/actions"
	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/calendar"
	"github.com/undeluro/kardan-alerts/internal/directory"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/finance"
	"github.com/undeluro/kardan-alerts/internal/notify"
	"github.com/undeluro/kardan-alerts/internal/platform/clock"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
	"github.com/undeluro/kardan-alerts/internal/privacy"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/runner"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

type Processor struct {
	Kafka       *kgo.Client // consumer-group client with auto-commit disabled
	Pool        *pgxpool.Pool
	Engine      *rules.Engine
	Directory   func() *directory.Directory
	Privacy     func() *privacy.Policy
	FX          func() *finance.FX
	Correlation func() *finance.Correlation
	Calendars   func() *calendar.Set
	Redis       *redis.Client // aggregate windows and absence state
	Runner      *runner.Runner
	Actions     func() *actions.Registry
	// ApprovalSecret signs one-click approval links in emails.
	ApprovalSecret string
	Throttler      *notify.Throttler
	Clock          *clock.Clock      // business time for working hours and on-call
	Fallbacks      map[string]string // channel -> fallback channel, e.g. sms -> email
	Renderer       notify.Renderer
	Publisher      *alerting.Publisher
	Schemas        *schema.Registry
	BatchSize      int
	Log            *slog.Logger
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

	notional  *float64 // USD
	impact    int
	winStart  *time.Time
	winEnd    *time.Time
	corrField string
	corrValue string

	kind   string // notification kind: alert (default) | digest | escalation
	digest string // appended to titles of digest notifications

	cutoffName   *string
	cutoffAt     *time.Time
	baseImpact   int
	uiOnly       bool          // downgraded outside market hours: no external notifications
	routes       []rules.Route // overrides the rule's routing (escalation steps)
	reasonPrefix string        // prepended to delivery reasons (escalation)
}

type recovery struct {
	rule   *rules.Compiled
	at     time.Time
	reason string
}

// newGroup prepares the alert for the first event of a fingerprint: UI
// presentation (with sensitive-field flags), money at risk, impact, digest
// window and incident key.
func (p *Processor) newGroup(c *rules.Compiled, fp string, doc *schema.Doc, sig string, corr *finance.Correlation) *group {
	env := doc.Env
	kinds := p.Schemas.Kinds(sig)
	classify := p.classifier(doc, kinds)
	g := &group{rule: c, fp: fp, first: doc, kinds: kinds, firstAt: env.ReceivedAt, recvAt: env.ReceivedAt,
		rendered: c.RenderWith(doc.Get, func(path string) bool { return classify(path) > privacy.Public })}
	if usd := doc.Get("$notionalUsd"); usd.Exists() {
		v := usd.Num
		g.notional = &v
	}
	if cal, off := c.During(); cal != "" && off != "suppress" && !p.calendarOpen(cal) {
		// Outside the rule's market hours: keep the alert, at a lower level, UI only.
		g.rendered.Severity, g.rendered.Color, g.uiOnly = off, rules.ColorFor(off), true
		g.rendered.Body = strings.TrimSpace(g.rendered.Body + "\n(outside " + cal + " hours: downgraded to " + off + ", UI only)")
	}
	g.baseImpact = finance.Impact(g.rendered.Severity, deref(g.notional), g.notional != nil)
	g.impact = g.baseImpact
	if d, ok := p.cutoffFor(c, doc); ok {
		name, at := d.Name+" ("+d.Calendar+")", d.At
		g.cutoffName, g.cutoffAt = &name, &at
		g.impact = min(100, g.baseImpact+finance.Urgency(at, p.Clock.Now()))
	}
	if c.Grouped() {
		s, e := c.WindowFor(env.ReceivedAt)
		g.winStart, g.winEnd = &s, &e
	}
	g.corrField, g.corrValue = corr.Key(doc)
	return g
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

// calendarOpen reports whether a calendar is open at business time "now".
// Unknown calendars count as open (the rule loader reports them).
func (p *Processor) calendarOpen(id string) bool {
	c, ok := p.cals().Get(id)
	return !ok || c.IsOpen(p.Clock.Now())
}

// cutoffFor finds the deadline an alert races against: the next cut-off
// for the event's currency ("auto") or a named cut-off.
func (p *Processor) cutoffFor(c *rules.Compiled, doc *schema.Doc) (calendar.Deadline, bool) {
	mode := c.CutoffMode()
	if mode == "" {
		return calendar.Deadline{}, false
	}
	set, now := p.cals(), p.Clock.Now()
	if mode == "auto" {
		return set.NextCutoff(doc.Get("$currency").String(), now)
	}
	return set.Named(mode, now)
}

type aggOp struct {
	rule   *rules.Compiled
	fp     string
	values []string
	doc    *schema.Doc
	sig    string
	value  float64
}

type absenceSeen struct {
	rule *rules.Compiled
	key  string // sender
	fp   string // fingerprint of today's / the heartbeat "missing" alert
}

// cals is the current calendar set (nil-safe).
func (p *Processor) cals() *calendar.Set {
	if p.Calendars == nil {
		return nil
	}
	return p.Calendars()
}

func (p *Processor) processBatch(ctx context.Context, recs []*kgo.Record) error {
	start := time.Now()
	rs := p.Engine.Current()
	fx, corr := p.FX(), p.Correlation()
	groups := map[string]*group{}
	recoveries := map[string]recovery{}
	observed := map[string]*schema.Observation{}
	var aggs []aggOp
	var seen []absenceSeen

	for _, rec := range recs {
		env := &event.Envelope{}
		if err := json.Unmarshal(rec.Value, env); err != nil {
			p.Log.Warn("skipping undecodable record", "partition", rec.Partition, "offset", rec.Offset, "err", err)
			continue
		}
		metrics.EventsProcessed.Inc()
		doc := schema.NewDoc(env)
		doc.FX = fx
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

		// A recovery signal (e.g. Alertmanager status=resolved) closes the
		// open alert instead of counting as another occurrence.
		recovered := map[string]bool{}
		for _, c := range rs.Recoveries(doc) {
			recovered[c.Rule.ID] = true
			fp := c.Fingerprint(doc)
			if r, ok := recoveries[fp]; !ok || env.ReceivedAt.After(r.at) {
				recoveries[fp] = recovery{rule: c, at: env.ReceivedAt}
			}
		}

		for _, c := range rs.Aggregates(doc) {
			values, fp := c.AggregateKey(doc)
			v := 1.0
			if f := c.Aggregate().SumField; f != "" {
				if r := doc.Get(f); r.Exists() {
					v = r.Float()
				} else {
					v = 0
				}
			}
			aggs = append(aggs, aggOp{rule: c, fp: fp, values: values, doc: doc, sig: sig, value: v})
		}
		for _, c := range rs.Absences(doc) {
			key := orNoSender(c.AbsenceKey(doc))
			day := ""
			if spec := c.Absence(); spec.Deadline >= 0 {
				if cal, ok := p.cals().Get(spec.Calendar); ok {
					day = p.Clock.Now().In(cal.Location()).Format("2006-01-02")
				}
			}
			seen = append(seen, absenceSeen{rule: c, key: key, fp: c.AbsenceFingerprint(key, day)})
		}

		for _, c := range rs.Match(doc) {
			if recovered[c.Rule.ID] {
				continue
			}
			if cal, off := c.During(); cal != "" && off == "suppress" && !p.calendarOpen(cal) {
				continue // outside market hours and the rule says to stay quiet
			}
			metrics.RuleMatches.WithLabelValues(c.Rule.ID).Inc()
			fp := c.Fingerprint(doc)
			g := groups[fp]
			if g == nil {
				g = p.newGroup(c, fp, doc, sig, corr)
				groups[fp] = g
			}
			g.count++
			if env.ReceivedAt.After(g.lastAt) {
				g.lastAt, g.lastID = env.ReceivedAt, env.ID
			}
			if env.ReceivedAt.Before(g.recvAt) {
				g.recvAt = env.ReceivedAt
			}
			// A later occurrence may carry more money at risk.
			if usd := doc.Get("$notionalUsd"); usd.Exists() && usd.Num > deref(g.notional) {
				v := usd.Num
				g.notional = &v
				g.impact = max(g.impact, finance.Impact(g.rendered.Severity, v, true))
			}
		}
	}
	// The expected event arrived: close the "missing" alert for that sender.
	reopened, err := p.recordAbsenceSeen(ctx, seen)
	if err != nil {
		return err
	}
	for _, s := range reopened {
		recoveries[s.fp] = recovery{rule: s.rule, at: time.Now(), reason: "the expected event arrived"}
	}
	if err := p.applyAggregates(ctx, aggs, groups, corr); err != nil {
		return err
	}
	if len(groups) == 0 && len(recoveries) == 0 {
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
	recFPs := make([]string, 0, len(recoveries))
	for fp := range recoveries {
		recFPs = append(recFPs, fp)
	}
	sort.Strings(recFPs)

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
			NotionalUSD:  g.notional,
			Impact:       g.impact,
			WindowStart:  g.winStart,
			WindowEnd:    g.winEnd,
			CutoffName:   g.cutoffName,
			CutoffAt:     g.cutoffAt,
			BaseImpact:   g.baseImpact,
			Vars:         schema.EncodeVars(g.first.Vars),
		}, Refresh: g.rule.Kind() == rules.KindAggregate}
	}

	var updates []alerting.Update
	err = pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		updates = updates[:0]
		var outbox []alerting.Notification
		dir := p.Directory()
		now := p.Clock.Now()

		if len(inputs) > 0 {
			results, err := alerting.Upsert(ctx, tx, inputs)
			if err != nil {
				return err
			}
			var links []alerting.IncidentInput
			created := map[string]*alerting.Alert{}
			var createdOrder []string
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
				if g.corrField != "" {
					links = append(links, alerting.IncidentInput{NewID: event.NewID(), AlertID: a.ID, KeyField: g.corrField,
						KeyValue: g.corrValue, Team: a.Team, Severity: a.Severity, NotionalUSD: deref(a.NotionalUSD),
						Impact: a.Impact, At: a.FirstSeen})
				}
				// Windowed alerts notify once, as a digest, when the window closes;
				// downgraded (outside hours) alerts stay in the UI.
				if !g.rule.Grouped() && !g.uiOnly {
					outbox = append(outbox, p.notificationsFor(ctx, &a, g, dir, now)...)
				}
				ac := a
				created[a.ID] = &ac
				createdOrder = append(createdOrder, a.ID)
			}
			incidents, err := alerting.LinkIncidents(ctx, tx, links)
			if err != nil {
				return err
			}
			for _, id := range createdOrder {
				if err := p.startRunbooks(ctx, tx, created[id]); err != nil {
					return err
				}
			}
			for _, id := range createdOrder {
				a := created[id]
				if inc, ok := incidents[id]; ok {
					a.IncidentID = &inc
				}
				updates = append(updates, alerting.Update{Kind: "created", Team: a.Team, Alert: a,
					ReceivedAt: groups[a.Fingerprint].recvAt})
			}
		}

		for _, fp := range recFPs {
			why := recoveries[fp].reason
			if why == "" {
				why = "recovery event received"
			}
			a, err := alerting.ResolveFingerprint(ctx, tx, fp, "auto: "+why)
			if alerting.IsNotFound(err) {
				continue // nothing open: recovery for an alert we never opened
			}
			if err != nil {
				return err
			}
			clear, err := p.allClear(ctx, tx, a, why)
			if err != nil {
				return err
			}
			outbox = append(outbox, clear...)
			metrics.AlertsAutoResolved.WithLabelValues("recovery").Inc()
			a.Sample, a.SampleRaw = nil, ""
			updates = append(updates, alerting.Update{Kind: "status", Team: a.Team, Alert: a})
		}
		return alerting.InsertNotifications(ctx, tx, outbox)
	})
	if err != nil {
		return err
	}
	p.forgetAbsences(ctx, reopened)

	for _, u := range updates {
		switch u.Kind {
		case "created":
			metrics.AlertsCreated.WithLabelValues(u.Team).Inc()
		case "count":
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

// allClear tells everyone who was paged by SMS, phone or email that the
// alert resolved itself. Phone recipients get an SMS: nobody wants a call to
// say all is well. Texts reuse what each person was sent, so masking holds.
func (p *Processor) allClear(ctx context.Context, q alerting.Querier, a *alerting.Alert, reason string) ([]alerting.Notification, error) {
	paged, err := alerting.PagedFor(ctx, q, a.ID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []alerting.Notification
	for _, pg := range paged {
		ch := pg.Channel
		if ch == "phone" {
			ch = "sms"
		}
		if seen[pg.Recipient+"|"+ch] {
			continue
		}
		seen[pg.Recipient+"|"+ch] = true
		msg := p.Renderer.RenderAllClear(ch, a.ID, pg.Subject, reason)
		out = append(out, alerting.Notification{
			ID: event.NewID(), AlertID: a.ID, Recipient: pg.Recipient, Channel: ch, Address: pg.Address,
			Subject: msg.Subject, BodyText: msg.BodyText, BodyHTML: msg.BodyHTML, EventReceivedAt: time.Now(),
			Kind: "resolved", Reason: "all clear: " + reason,
		})
	}
	return out, nil
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
//   - per-person throttling holds messages over the limit for a digest;
//   - a fallback channel is rendered too and parked as standby, so if the
//     primary channel fails the dispatcher can switch without re-rendering.
//
// "ui" needs no row: the live feed shows every alert.
func (p *Processor) notificationsFor(ctx context.Context, a *alerting.Alert, g *group, dir *directory.Directory, now time.Time) []alerting.Notification {
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
	routes := g.rule.Rule.Routing
	if g.routes != nil {
		routes = g.routes
	}
	for _, route := range routes {
		people, why := dir.Resolve(route.To, now)
		why = join(g.reasonPrefix, why)
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
	books := map[privacy.Level][]notify.RunbookSummary{}
	attached := p.attachedRunbooks(a, g)
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
			if g.digest != "" {
				cp.Title += " — " + g.digest
			}
			if g.kind == "escalation" {
				cp.Title = "ESCALATED: " + cp.Title
			}
			ma = &cp
			masked[lvl] = ma
			books[lvl] = p.runbookSummaries(attached, a, get) // steps masked like the rest
		}
		return p.Renderer.RenderWithRunbooks(channel, ma, books[lvl])
	}

	kind := g.kind
	if kind == "" {
		kind = "alert"
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
			Reason: d.reason, Kind: kind,
		}
		if held, until := p.Throttler.Check(ctx, d.person.ID, d.channel, a.Severity, time.Now()); held {
			primary.Status, primary.NextAttemptAt = "held", until
			primary.Reason = join(d.reason, "held by throttling, included in a digest at "+until.Format("15:04 MST"))
			metrics.NotificationsHeld.WithLabelValues(d.channel).Inc()
			out = append(out, primary)
			continue
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
				Status: "standby", FallbackFor: primary.ID, Reason: "fallback if " + d.channel + " fails", Kind: kind,
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

func windowLabel(start, end *time.Time) string {
	if start == nil || end == nil {
		return ""
	}
	layout := "15:04"
	if start.Second() != 0 || end.Second() != 0 {
		layout = "15:04:05"
	}
	return fmt.Sprintf("%s–%s UTC", start.UTC().Format(layout), end.UTC().Format(layout))
}

func orNoSender(key string) string {
	if key == "" {
		return noSender
	}
	return key
}

// recordAbsenceSeen remembers when each expected sender last reported (real
// and business time: heartbeats measure real silence, deadlines compare with
// the business day) and returns the senders whose "missing" alert is open.
func (p *Processor) recordAbsenceSeen(ctx context.Context, seen []absenceSeen) ([]absenceSeen, error) {
	if len(seen) == 0 || p.Redis == nil {
		return nil, nil
	}
	real, biz := time.Now().UnixMilli(), p.Clock.Now().UnixMilli()
	stamp := strconv.FormatInt(real, 10) + ":" + strconv.FormatInt(biz, 10)
	pipe := p.Redis.Pipeline()
	open := map[string]*redis.StringSliceCmd{}
	for _, s := range seen {
		id := s.rule.Rule.ID
		pipe.HSet(ctx, absenceKey(id), s.key, stamp)
		if open[id] == nil {
			open[id] = pipe.SMembers(ctx, absenceOpenKey(id))
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	var out []absenceSeen
	done := map[string]bool{}
	for _, s := range seen {
		if done[s.fp] {
			continue
		}
		for _, fp := range open[s.rule.Rule.ID].Val() {
			if fp == s.fp {
				out = append(out, s)
				done[s.fp] = true
			}
		}
	}
	return out, nil
}

// forgetAbsences drops resolved "missing" alerts from the open sets.
func (p *Processor) forgetAbsences(ctx context.Context, resolved []absenceSeen) {
	if len(resolved) == 0 {
		return
	}
	pipe := p.Redis.Pipeline()
	for _, s := range resolved {
		pipe.SRem(ctx, absenceOpenKey(s.rule.Rule.ID), s.fp)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		p.Log.Warn("forget absence alerts", "err", err)
	}
}

// applyAggregates adds each event to its rule's sliding window. When a key's
// window total reaches the threshold, its aggregate alert opens (or, if open,
// takes the new total: the alert count is the peak events in the window).
func (p *Processor) applyAggregates(ctx context.Context, ops []aggOp, groups map[string]*group, corr *finance.Correlation) error {
	if len(ops) == 0 || p.Redis == nil {
		return nil
	}
	run := func(c redis.Scripter, op aggOp) *redis.Cmd {
		member := op.doc.Env.ID + "|" + strconv.FormatFloat(op.value, 'f', -1, 64)
		return aggAdd.Run(ctx, c, []string{aggKey(op.rule.Rule.ID, op.fp)},
			op.doc.Env.ReceivedAt.UnixMilli(), op.rule.Aggregate().Window.Milliseconds(), member)
	}
	// Load the script once so the pipelined EVALSHAs below cannot miss.
	if err := aggAdd.Load(ctx, p.Redis).Err(); err != nil {
		return err
	}
	pipe := p.Redis.Pipeline()
	cmds := make([]*redis.Cmd, len(ops))
	for i, op := range ops {
		cmds[i] = run(pipe, op)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	// The last crossing per key carries the running total for this batch.
	type crossing struct {
		op    aggOp
		total windowTotal
		value float64
	}
	last := map[string]crossing{}
	var order []string
	for i, op := range ops {
		total, err := parseTotal(cmds[i].Val())
		if err != nil {
			return err
		}
		spec := op.rule.Aggregate()
		value := float64(total.Count)
		if spec.SumField != "" {
			value = total.Sum
		}
		if value < spec.Threshold {
			continue
		}
		if _, ok := last[op.fp]; !ok {
			order = append(order, op.fp)
		}
		last[op.fp] = crossing{op, total, value}
	}
	for _, fp := range order {
		x := last[fp]
		spec := x.op.rule.Aggregate()
		vars := map[string]gjson.Result{
			"$count":     schema.Num(float64(x.total.Count)),
			"$sum":       schema.Num(x.total.Sum),
			"$value":     schema.Num(x.value),
			"$threshold": schema.Num(spec.Threshold),
			"$window":    schema.Str(rules.HumanWindow(spec.Window)),
			"$key":       schema.Str(strings.Join(x.op.values, " / ")),
		}
		if spec.SumField == "$notionalUsd" {
			vars["$notionalUsd"] = schema.Num(x.total.Sum) // money at risk is the window total
		}
		g := p.newGroup(x.op.rule, fp, x.op.doc.WithVars(vars), x.op.sig, corr)
		env := x.op.doc.Env
		g.count, g.lastAt, g.lastID = x.total.Count, env.ReceivedAt, env.ID
		metrics.RuleMatches.WithLabelValues(x.op.rule.Rule.ID).Inc()
		groups[fp] = g
	}
	return nil
}
