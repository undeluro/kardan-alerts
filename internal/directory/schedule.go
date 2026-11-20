package directory

import (
	"fmt"
	"strings"
	"time"

	"github.com/undeluro/kardan-alerts/internal/schema"
)

// Window is a daily time range such as "08:00-20:00", optionally limited to
// days ("Mon-Fri", "Sat,Sun"). A range whose end is before its start wraps
// midnight ("22:00-06:00").
type Window struct {
	Between string `yaml:"between,omitempty" json:"between,omitempty"`
	Days    string `yaml:"days,omitempty" json:"days,omitempty"`
}

type parsedWindow struct {
	start, end int // minutes since midnight
	days       map[time.Weekday]bool
	always     bool
}

func parseWindow(w Window) (parsedWindow, error) {
	pw := parsedWindow{}
	if w.Between == "" {
		pw.always = true
	} else {
		a, b, ok := strings.Cut(w.Between, "-")
		if !ok {
			return pw, fmt.Errorf("between %q must look like 08:00-20:00", w.Between)
		}
		var err error
		if pw.start, err = parseClock(a); err != nil {
			return pw, err
		}
		if pw.end, err = parseClock(b); err != nil {
			return pw, err
		}
	}
	if w.Days != "" {
		days, err := parseDays(w.Days)
		if err != nil {
			return pw, err
		}
		pw.days = days
	}
	return pw, nil
}

func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("time %q must be HH:MM", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

var weekdays = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday,
	"wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}

func parseDays(s string) (map[time.Weekday]bool, error) {
	out := map[time.Weekday]bool{}
	for _, part := range strings.Split(strings.ToLower(s), ",") {
		part = strings.TrimSpace(part)
		if a, b, ok := strings.Cut(part, "-"); ok {
			from, ok1 := weekdays[strings.TrimSpace(a)[:min(3, len(strings.TrimSpace(a)))]]
			to, ok2 := weekdays[strings.TrimSpace(b)[:min(3, len(strings.TrimSpace(b)))]]
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("days %q: use names like Mon-Fri or Sat,Sun", s)
			}
			for d := from; ; d = (d + 1) % 7 {
				out[d] = true
				if d == to {
					break
				}
			}
			continue
		}
		if len(part) < 3 {
			return nil, fmt.Errorf("days %q: use names like Mon-Fri or Sat,Sun", s)
		}
		d, ok := weekdays[part[:3]]
		if !ok {
			return nil, fmt.Errorf("days %q: unknown day %q", s, part)
		}
		out[d] = true
	}
	return out, nil
}

// contains reports whether t (already in the right time zone) is inside.
func (w parsedWindow) contains(t time.Time) bool {
	if w.days != nil && !w.days[t.Weekday()] {
		// A wrapping window that started yesterday still counts after midnight.
		if !(w.start > w.end && w.days[(t.Weekday()+6)%7] && t.Hour()*60+t.Minute() < w.end) {
			return false
		}
	}
	if w.always {
		return true
	}
	m := t.Hour()*60 + t.Minute()
	if w.start <= w.end {
		return m >= w.start && m < w.end
	}
	return m >= w.start || m < w.end
}

// Preference maps alert severity (this level or higher) to channels, within
// an optional time window in the person's time zone. The first matching
// entry wins, so list the most specific first:
//
//   - { severity: critical, channels: [sms], between: "08:00-20:00" }
//   - { severity: critical, channels: [email] }
type Preference struct {
	Severity string   `yaml:"severity,omitempty" json:"severity,omitempty"`
	Channels []string `yaml:"channels" json:"channels"`
	Window   `yaml:",inline"`

	level  schema.Level
	window parsedWindow
}

// DefaultPreferences apply when neither the person nor the team sets any:
// critical -> phone + SMS, warning -> email, info -> UI only.
var DefaultPreferences = []Preference{
	{Severity: "critical", Channels: []string{"phone", "sms"}},
	{Severity: "warning", Channels: []string{"email"}},
	{Severity: "info", Channels: []string{"ui"}},
}

func compilePreferences(prefs []Preference, where string) ([]Preference, error) {
	out := make([]Preference, len(prefs))
	for i, p := range prefs {
		at := fmt.Sprintf("%s.preferences[%d]", where, i)
		if p.Severity != "" {
			p.level = schema.ParseLevel(p.Severity)
			if p.level == schema.LevelUnknown {
				return nil, fmt.Errorf("%s: unknown severity %q", at, p.Severity)
			}
		}
		if len(p.Channels) == 0 {
			return nil, fmt.Errorf("%s: channels are required", at)
		}
		for _, ch := range p.Channels {
			if !validChannel(ch) {
				return nil, fmt.Errorf("%s: unknown channel %q (email, sms, phone, ui)", at, ch)
			}
		}
		w, err := parseWindow(p.Window)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", at, err)
		}
		p.window = w
		out[i] = p
	}
	return out, nil
}

func validChannel(ch string) bool {
	switch ch {
	case "email", "sms", "phone", "ui":
		return true
	}
	return false
}

// pick returns the channels of the first preference matching severity and time.
func pick(prefs []Preference, sev schema.Level, local time.Time) ([]string, *Preference) {
	for i := range prefs {
		p := &prefs[i]
		if p.level != schema.LevelUnknown && sev < p.level {
			continue
		}
		if !p.window.contains(local) {
			continue
		}
		return p.Channels, p
	}
	return nil, nil
}

// OnCall describes who is reachable for a team right now. Resolution order:
// an override covering now, then a follow-the-sun shift covering now, then the
// rotation, then the first team member.
type OnCall struct {
	Timezone  string     `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	Rotation  *Rotation  `yaml:"rotation,omitempty" json:"rotation,omitempty"`
	Shifts    []Shift    `yaml:"shifts,omitempty" json:"shifts,omitempty"`
	Overrides []Override `yaml:"overrides,omitempty" json:"overrides,omitempty"`

	loc *time.Location
}

// Rotation hands over every day or week at the time of Start.
type Rotation struct {
	Every  string   `yaml:"every" json:"every"` // day | week
	Start  string   `yaml:"start" json:"start"` // first hand-over, local time: 2026-11-02T09:00
	People []string `yaml:"people" json:"people"`

	start time.Time
}

// Shift is a follow-the-sun slot: Person covers Between in their time zone.
type Shift struct {
	Person   string `yaml:"person" json:"person"`
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"` // default: the person's
	Window   `yaml:",inline"`

	loc    *time.Location
	window parsedWindow
}

// Override puts someone on call for a fixed period (holiday cover, incidents).
type Override struct {
	Person string    `yaml:"person" json:"person"`
	From   time.Time `yaml:"from" json:"from"`
	To     time.Time `yaml:"to" json:"to"`
}

// OnCallNow is the resolved on-call person and why.
type OnCallNow struct {
	Team   string `json:"team"`
	Person Person `json:"person"`
	Reason string `json:"reason"`
}

func (d *Directory) compileOnCall(t *Team) error {
	oc := t.OnCall
	if oc == nil {
		return nil
	}
	at := "team " + t.ID + " onCall"
	tz := oc.Timezone
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return fmt.Errorf("%s: timezone %q: %w", at, tz, err)
	}
	oc.loc = loc
	if r := oc.Rotation; r != nil {
		if r.Every != "day" && r.Every != "week" {
			return fmt.Errorf("%s.rotation: every must be day or week", at)
		}
		if len(r.People) == 0 {
			return fmt.Errorf("%s.rotation: people are required", at)
		}
		for _, p := range r.People {
			if _, ok := d.people[p]; !ok {
				return fmt.Errorf("%s.rotation: unknown person %q", at, p)
			}
		}
		r.start, err = time.ParseInLocation("2006-01-02T15:04", r.Start, loc)
		if err != nil {
			return fmt.Errorf("%s.rotation: start %q must look like 2026-11-02T09:00", at, r.Start)
		}
	}
	for i := range oc.Shifts {
		s := &oc.Shifts[i]
		p, ok := d.people[s.Person]
		if !ok {
			return fmt.Errorf("%s.shifts[%d]: unknown person %q", at, i, s.Person)
		}
		stz := s.Timezone
		if stz == "" {
			stz = p.Timezone
		}
		if stz == "" {
			stz = tz
		}
		if s.loc, err = time.LoadLocation(stz); err != nil {
			return fmt.Errorf("%s.shifts[%d]: timezone %q: %w", at, i, stz, err)
		}
		if s.window, err = parseWindow(s.Window); err != nil {
			return fmt.Errorf("%s.shifts[%d]: %w", at, i, err)
		}
	}
	for i, o := range oc.Overrides {
		if _, ok := d.people[o.Person]; !ok {
			return fmt.Errorf("%s.overrides[%d]: unknown person %q", at, i, o.Person)
		}
		if !o.To.After(o.From) {
			return fmt.Errorf("%s.overrides[%d]: to must be after from", at, i)
		}
	}
	return nil
}

// WhoIsOnCall resolves a team's on-call person at time now.
func (d *Directory) WhoIsOnCall(teamID string, now time.Time) (OnCallNow, bool) {
	t, ok := d.teams[teamID]
	if !ok {
		return OnCallNow{}, false
	}
	res := OnCallNow{Team: teamID}
	oc := t.OnCall
	if oc != nil {
		for _, o := range oc.Overrides {
			if !now.Before(o.From) && now.Before(o.To) {
				res.Person, res.Reason = d.people[o.Person], "override until "+o.To.In(oc.loc).Format("Mon 02 Jan 15:04 MST")
				return res, true
			}
		}
		for _, s := range oc.Shifts {
			local := now.In(s.loc)
			if s.window.contains(local) {
				res.Person = d.people[s.Person]
				res.Reason = fmt.Sprintf("follow-the-sun shift %s %s", s.Between, s.loc.String())
				return res, true
			}
		}
		if r := oc.Rotation; r != nil {
			period := 7 * 24 * time.Hour
			if r.Every == "day" {
				period = 24 * time.Hour
			}
			n := int(now.Sub(r.start) / period)
			if now.Before(r.start) {
				n = 0
			}
			idx := n % len(r.People)
			next := r.start.Add(time.Duration(n+1) * period)
			res.Person = d.people[r.People[idx]]
			res.Reason = fmt.Sprintf("%s rotation, hands over %s", map[string]string{"day": "daily", "week": "weekly"}[r.Every],
				next.In(oc.loc).Format("Mon 02 Jan 15:04 MST"))
			return res, true
		}
	}
	if len(t.Members) == 0 {
		return OnCallNow{}, false
	}
	res.Person, res.Reason = d.people[t.Members[0]], "no schedule: first team member"
	return res, true
}

// ChannelsFor resolves "preferences" for a person: their own preferences,
// else their team's, else the defaults; evaluated in the person's time zone.
func (d *Directory) ChannelsFor(p Person, teamID, severity string, now time.Time) (channels []string, reason string) {
	sev := schema.ParseLevel(severity)
	loc := time.UTC
	if p.loc != nil {
		loc = p.loc
	}
	local := now.In(loc)
	sources := []struct {
		prefs []Preference
		label string
	}{{p.Preferences, "personal preference"}}
	if t, ok := d.teams[teamID]; ok {
		sources = append(sources, struct {
			prefs []Preference
			label string
		}{t.Preferences, "team " + t.ID + " preference"})
	}
	sources = append(sources, struct {
		prefs []Preference
		label string
	}{d.defaults, "default preference"})
	for _, s := range sources {
		if len(s.prefs) == 0 {
			continue
		}
		if chs, pref := pick(s.prefs, sev, local); pref != nil {
			why := s.label
			if pref.Severity != "" {
				why += " for " + pref.Severity
			}
			if pref.Between != "" {
				why += " " + pref.Between + " " + loc.String()
			}
			return chs, why + " (local time " + local.Format("15:04") + ")"
		}
	}
	return []string{"ui"}, "no matching preference: UI only"
}
