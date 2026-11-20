// Package calendar knows when markets and payment systems are open and when
// their cut-offs are: business days, holidays, opening hours and named
// deadlines per currency. Rules use it for "only during market hours",
// absence deadlines ("by 18:30 on a business day") and cut-off urgency.
package calendar

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

type Calendar struct {
	ID           string   `yaml:"id" json:"id"`
	Name         string   `yaml:"name" json:"name"`
	Timezone     string   `yaml:"timezone" json:"timezone"`
	BusinessDays string   `yaml:"businessDays,omitempty" json:"businessDays,omitempty"` // default Mon-Fri
	Holidays     []string `yaml:"holidays,omitempty" json:"holidays,omitempty"`         // YYYY-MM-DD, local
	Hours        string   `yaml:"hours,omitempty" json:"hours,omitempty"`               // "07:00-18:00"; empty = all day
	Cutoffs      []Cutoff `yaml:"cutoffs,omitempty" json:"cutoffs,omitempty"`

	loc        *time.Location
	days       map[time.Weekday]bool
	holidays   map[string]bool
	open, shut int // minutes since midnight
	allDay     bool
}

// Cutoff is a daily deadline, e.g. "T2 customer payments at 17:00 for EUR".
type Cutoff struct {
	Name       string   `yaml:"name" json:"name"`
	At         string   `yaml:"at" json:"at"`
	Currencies []string `yaml:"currencies,omitempty" json:"currencies,omitempty"`

	minute int
}

// Set is the loaded collection, from config/calendars.yaml.
type Set struct {
	Calendars []*Calendar `yaml:"calendars" json:"calendars"`
	Note      string      `yaml:"note,omitempty" json:"note,omitempty"`

	byID map[string]*Calendar
}

// Load reads calendars.yaml; a missing file yields an empty set.
func Load(path string) (*Set, error) {
	s := &Set{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := yaml.Unmarshal(data, s); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return s, s.compile()
}

func (s *Set) compile() error {
	s.byID = map[string]*Calendar{}
	for i, c := range s.Calendars {
		at := fmt.Sprintf("calendars[%d] (%s)", i, c.ID)
		if c.ID == "" {
			return fmt.Errorf("%s: id is required", at)
		}
		if _, dup := s.byID[c.ID]; dup {
			return fmt.Errorf("%s: duplicate id", at)
		}
		tz := c.Timezone
		if tz == "" {
			tz = "UTC"
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return fmt.Errorf("%s: timezone %q: %w", at, tz, err)
		}
		c.loc = loc
		days := c.BusinessDays
		if days == "" {
			days = "Mon-Fri"
		}
		if c.days, err = parseDays(days); err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
		c.holidays = map[string]bool{}
		for _, h := range c.Holidays {
			if _, err := time.Parse("2006-01-02", h); err != nil {
				return fmt.Errorf("%s: holiday %q must be YYYY-MM-DD", at, h)
			}
			c.holidays[h] = true
		}
		if c.Hours == "" {
			c.allDay = true
		} else {
			a, b, ok := strings.Cut(c.Hours, "-")
			if !ok {
				return fmt.Errorf("%s: hours %q must look like 07:00-18:00", at, c.Hours)
			}
			if c.open, err = clock(a); err != nil {
				return fmt.Errorf("%s: %w", at, err)
			}
			if c.shut, err = clock(b); err != nil {
				return fmt.Errorf("%s: %w", at, err)
			}
		}
		for j := range c.Cutoffs {
			co := &c.Cutoffs[j]
			if co.Name == "" {
				return fmt.Errorf("%s: cutoffs[%d] needs a name", at, j)
			}
			if co.minute, err = clock(co.At); err != nil {
				return fmt.Errorf("%s: cutoff %q: %w", at, co.Name, err)
			}
			for k, ccy := range co.Currencies {
				co.Currencies[k] = strings.ToUpper(strings.TrimSpace(ccy))
			}
		}
		s.byID[c.ID] = c
	}
	return nil
}

func (s *Set) Get(id string) (*Calendar, bool) {
	if s == nil {
		return nil, false
	}
	c, ok := s.byID[id]
	return c, ok
}

func (c *Calendar) Location() *time.Location { return c.loc }

// IsBusinessDay reports whether the local date of t is a working day.
func (c *Calendar) IsBusinessDay(t time.Time) bool {
	l := t.In(c.loc)
	return c.days[l.Weekday()] && !c.holidays[l.Format("2006-01-02")]
}

// IsOpen reports whether t is within opening hours on a business day.
func (c *Calendar) IsOpen(t time.Time) bool {
	if !c.IsBusinessDay(t) {
		return false
	}
	if c.allDay {
		return true
	}
	l := t.In(c.loc)
	m := l.Hour()*60 + l.Minute()
	return m >= c.open && m < c.shut
}

// At returns local time hh:mm on t's local date.
func (c *Calendar) At(t time.Time, minute int) time.Time {
	l := t.In(c.loc)
	return time.Date(l.Year(), l.Month(), l.Day(), minute/60, minute%60, 0, 0, c.loc)
}

// DayStart is local midnight of t's date.
func (c *Calendar) DayStart(t time.Time) time.Time { return c.At(t, 0) }

// NextBusinessDay returns local midnight of the next business day after t's date.
func (c *Calendar) NextBusinessDay(t time.Time) time.Time {
	d := c.DayStart(t)
	for i := 0; i < 14; i++ {
		d = d.AddDate(0, 0, 1)
		if c.IsBusinessDay(d.Add(12 * time.Hour)) {
			return d
		}
	}
	return d
}

// Deadline is a resolved cut-off instance.
type Deadline struct {
	Calendar string    `json:"calendar"`
	Name     string    `json:"name"`
	At       time.Time `json:"at"`
}

// NextCutoff returns the earliest cut-off for a currency at or after t
// (today if it has not passed and today is a business day, else the next
// business day), across all calendars.
func (s *Set) NextCutoff(ccy string, t time.Time) (Deadline, bool) {
	ccy = strings.ToUpper(strings.TrimSpace(ccy))
	if s == nil || ccy == "" {
		return Deadline{}, false
	}
	var best Deadline
	found := false
	for _, c := range s.Calendars {
		for _, co := range c.Cutoffs {
			if !contains(co.Currencies, ccy) {
				continue
			}
			at := c.At(t, co.minute)
			if !c.IsBusinessDay(t) || !at.After(t) {
				at = c.At(c.NextBusinessDay(t).Add(12*time.Hour), co.minute)
			}
			if !found || at.Before(best.At) {
				best, found = Deadline{Calendar: c.ID, Name: co.Name, At: at}, true
			}
		}
	}
	return best, found
}

// Named finds a cut-off by name and returns its next instance.
func (s *Set) Named(name string, t time.Time) (Deadline, bool) {
	if s == nil {
		return Deadline{}, false
	}
	for _, c := range s.Calendars {
		for _, co := range c.Cutoffs {
			if strings.EqualFold(co.Name, name) {
				at := c.At(t, co.minute)
				if !c.IsBusinessDay(t) || !at.After(t) {
					at = c.At(c.NextBusinessDay(t).Add(12*time.Hour), co.minute)
				}
				return Deadline{Calendar: c.ID, Name: co.Name, At: at}, true
			}
		}
	}
	return Deadline{}, false
}

// Status summarises a calendar now, for the UI.
type Status struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Timezone    string     `json:"timezone"`
	LocalTime   string     `json:"localTime"`
	BusinessDay bool       `json:"businessDay"`
	Open        bool       `json:"open"`
	Hours       string     `json:"hours,omitempty"`
	Holiday     bool       `json:"holiday"`
	Cutoffs     []Deadline `json:"cutoffs"`
}

func (s *Set) Status(now time.Time) []Status {
	if s == nil {
		return nil
	}
	out := make([]Status, 0, len(s.Calendars))
	for _, c := range s.Calendars {
		l := now.In(c.loc)
		st := Status{ID: c.ID, Name: c.Name, Timezone: c.loc.String(), LocalTime: l.Format("Mon 15:04"),
			BusinessDay: c.IsBusinessDay(now), Open: c.IsOpen(now), Hours: c.Hours,
			Holiday: c.holidays[l.Format("2006-01-02")], Cutoffs: []Deadline{}}
		for _, co := range c.Cutoffs {
			if d, ok := s.Named(co.Name, now); ok {
				st.Cutoffs = append(st.Cutoffs, d)
			}
		}
		sort.Slice(st.Cutoffs, func(i, j int) bool { return st.Cutoffs[i].At.Before(st.Cutoffs[j].At) })
		out = append(out, st)
	}
	return out
}

func clock(s string) (int, error) {
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
			from, ok1 := weekdays[prefix3(a)]
			to, ok2 := weekdays[prefix3(b)]
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("businessDays %q: use names like Mon-Fri", s)
			}
			for d := from; ; d = (d + 1) % 7 {
				out[d] = true
				if d == to {
					break
				}
			}
			continue
		}
		d, ok := weekdays[prefix3(part)]
		if !ok {
			return nil, fmt.Errorf("businessDays %q: unknown day %q", s, part)
		}
		out[d] = true
	}
	return out, nil
}

func prefix3(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		return s[:3]
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
