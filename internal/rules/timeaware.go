package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/undeluro/kardan-alerts/internal/schema"
)

const (
	KindEvent     = "event"
	KindAggregate = "aggregate"
	KindAbsence   = "absence"
)

// timeAware holds the compiled time-related settings of a rule.
type timeAware struct {
	kind string

	aggBy        []string
	aggWindow    time.Duration
	aggSum       string
	aggThreshold float64

	absBy       string
	absExpect   []string
	absDeadline int // minutes since local midnight; -1 = heartbeat mode
	absEvery    time.Duration

	duringCalendar string
	duringOff      string // "suppress" or a severity

	cutoff     string // "", "auto" or a cut-off name
	escalation []Escalation
}

// Escalation is a compiled escalation step.
type Escalation struct {
	After time.Duration
	Route Route
}

func (c *Compiled) compileTimeAware(r Rule) error {
	t := &c.timeAware
	t.kind = r.Kind
	if t.kind == "" {
		t.kind = KindEvent
	}
	switch t.kind {
	case KindEvent:
		if r.Aggregate != nil || r.Absence != nil {
			return fmt.Errorf("aggregate/absence settings need kind: aggregate or kind: absence")
		}
	case KindAggregate:
		a := r.Aggregate
		if a == nil {
			return fmt.Errorf("kind: aggregate needs an aggregate block (window, threshold, optional by and sum)")
		}
		w, err := time.ParseDuration(a.Window)
		if err != nil || w < time.Second || w > 7*24*time.Hour {
			return fmt.Errorf("aggregate.window %q must be a duration between 1s and 7 days, like 1h", a.Window)
		}
		if a.Threshold <= 0 {
			return fmt.Errorf("aggregate.threshold must be positive")
		}
		if r.Group != nil || (r.AutoResolve != nil && r.AutoResolve.When != nil) {
			return fmt.Errorf("aggregate rules cannot use group or autoResolve.when (they resolve when the total drops below the threshold)")
		}
		t.aggBy, t.aggWindow, t.aggSum, t.aggThreshold = a.By, w, a.Sum, a.Threshold
	case KindAbsence:
		a := r.Absence
		if a == nil {
			return fmt.Errorf("kind: absence needs an absence block (deadline + calendar, or every)")
		}
		if (a.Deadline == "") == (a.Every == "") {
			return fmt.Errorf("absence needs exactly one of deadline (daily, with a calendar) or every (heartbeat)")
		}
		t.absDeadline = -1
		if a.Deadline != "" {
			if a.Calendar == "" {
				return fmt.Errorf("absence.deadline needs a calendar (business days and time zone)")
			}
			tm, err := time.Parse("15:04", a.Deadline)
			if err != nil {
				return fmt.Errorf("absence.deadline %q must be HH:MM", a.Deadline)
			}
			t.absDeadline = tm.Hour()*60 + tm.Minute()
		} else {
			d, err := time.ParseDuration(a.Every)
			if err != nil || d < 5*time.Second {
				return fmt.Errorf("absence.every %q must be a duration of at least 5s", a.Every)
			}
			t.absEvery = d
		}
		if a.By == "" && len(a.Expect) > 0 {
			return fmt.Errorf("absence.expect needs absence.by (the field that names the sender)")
		}
		if r.Group != nil || r.AutoResolve != nil {
			return fmt.Errorf("absence rules resolve themselves when the expected event arrives; remove group/autoResolve")
		}
		t.absBy, t.absExpect = a.By, a.Expect
		t.duringCalendar = a.Calendar
	default:
		return fmt.Errorf("unknown kind %q (event, aggregate, absence)", r.Kind)
	}

	if d := r.During; d != nil {
		if d.Calendar == "" {
			return fmt.Errorf("during.calendar is required")
		}
		off := strings.ToLower(strings.TrimSpace(d.Otherwise))
		if off != "suppress" && schema.ParseLevel(off) == schema.LevelUnknown {
			return fmt.Errorf("during.otherwise must be suppress or a severity (info, warning...)")
		}
		if t.kind == KindAbsence {
			return fmt.Errorf("absence rules use absence.calendar instead of during")
		}
		t.duringCalendar, t.duringOff = d.Calendar, off
	}

	t.cutoff = strings.TrimSpace(r.Cutoff)

	var prev time.Duration
	for i, e := range r.Escalation {
		d, err := time.ParseDuration(e.After)
		if err != nil || d <= 0 {
			return fmt.Errorf("escalation[%d].after %q must be a positive duration like 15m", i, e.After)
		}
		if d <= prev {
			return fmt.Errorf("escalation[%d].after must be later than the previous step", i)
		}
		prev = d
		if !strings.HasPrefix(e.To, "team:") && !strings.HasPrefix(e.To, "user:") && !strings.HasPrefix(e.To, "oncall:") {
			return fmt.Errorf("escalation[%d].to %q must start with team:, oncall: or user:", i, e.To)
		}
		if len(e.Channels) == 0 {
			return fmt.Errorf("escalation[%d].channels are required", i)
		}
		for _, ch := range e.Channels {
			switch ch {
			case "email", "sms", "phone", "preferences":
			default:
				return fmt.Errorf("escalation[%d]: unknown channel %q (email, sms, phone, preferences)", i, ch)
			}
		}
		t.escalation = append(t.escalation, Escalation{After: d, Route: Route{To: e.To, Channels: e.Channels}})
	}
	return nil
}

func (c *Compiled) Kind() string { return c.kind }

// HumanWindow formats a window for people: "10 min", "1h", "30s".
func HumanWindow(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%d min", d/time.Minute)
	default:
		return d.String()
	}
}

// Calendars lists the calendars the rule refers to (validated when rules load).
func (c *Compiled) Calendars() []string {
	if c.duringCalendar == "" {
		return nil
	}
	return []string{c.duringCalendar}
}

// During returns the calendar the rule is limited to and what happens outside
// its hours ("suppress" or a severity).
func (c *Compiled) During() (calendar, otherwise string) {
	if c.kind == KindAbsence {
		return "", ""
	}
	return c.duringCalendar, c.duringOff
}

// CutoffMode: "" (none), "auto" (from $currency) or a cut-off name.
func (c *Compiled) CutoffMode() string { return c.cutoff }

func (c *Compiled) EscalationSteps() []Escalation { return c.escalation }

// ---- aggregate ------------------------------------------------------------------

// AggregateSpec describes the window computation for an aggregate rule.
type AggregateSpec struct {
	Window    time.Duration
	SumField  string
	Threshold float64
}

func (c *Compiled) Aggregate() AggregateSpec {
	return AggregateSpec{Window: c.aggWindow, SumField: c.aggSum, Threshold: c.aggThreshold}
}

// AggregateKey returns the group key values and the alert fingerprint.
func (c *Compiled) AggregateKey(d *schema.Doc) (values []string, fingerprint string) {
	h := sha256.New()
	h.Write([]byte(c.Rule.ID))
	h.Write([]byte{0})
	for _, p := range c.aggBy {
		v := d.Get(p).String()
		values = append(values, v)
		h.Write([]byte(v))
		h.Write([]byte{0})
	}
	return values, hex.EncodeToString(h.Sum(nil))[:32]
}

// AggregateFields names the group-by fields (for synthetic payloads and UI).
func (c *Compiled) AggregateFields() []string { return c.aggBy }

// ---- absence --------------------------------------------------------------------

// AbsenceSpec describes an absence rule.
type AbsenceSpec struct {
	By       string
	Expect   []string
	Deadline int // minutes since midnight in the calendar's zone, -1 for heartbeat
	Every    time.Duration
	Calendar string
}

func (c *Compiled) Absence() AbsenceSpec {
	return AbsenceSpec{By: c.absBy, Expect: c.absExpect, Deadline: c.absDeadline, Every: c.absEvery, Calendar: c.duringCalendar}
}

// AbsenceKey is the sender an event reports for ("" when the rule has no by).
func (c *Compiled) AbsenceKey(d *schema.Doc) string {
	if c.absBy == "" {
		return ""
	}
	return d.Get(c.absBy).String()
}

// AbsenceFingerprint identifies "sender missing" alerts: one per sender for
// heartbeats, one per sender and business day for deadlines.
func (c *Compiled) AbsenceFingerprint(key, day string) string {
	h := sha256.Sum256([]byte(c.Rule.ID + "\x00absence\x00" + key + "\x00" + day))
	return hex.EncodeToString(h[:])[:32]
}
