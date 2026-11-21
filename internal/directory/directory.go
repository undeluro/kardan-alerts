// Package directory knows people, teams, how to reach them, when, and who is
// on call.
package directory

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

type Person struct {
	ID          string       `yaml:"id" json:"id"`
	Name        string       `yaml:"name" json:"name"`
	Email       string       `yaml:"email,omitempty" json:"email,omitempty"`
	Phone       string       `yaml:"phone,omitempty" json:"phone,omitempty"`
	Timezone    string       `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	Preferences []Preference `yaml:"preferences,omitempty" json:"preferences,omitempty"`

	loc *time.Location
}

type Team struct {
	ID          string       `yaml:"id" json:"id"`
	Name        string       `yaml:"name" json:"name"`
	Members     []string     `yaml:"members" json:"members"`
	Preferences []Preference `yaml:"preferences,omitempty" json:"preferences,omitempty"`
	OnCall      *OnCall      `yaml:"onCall,omitempty" json:"onCall,omitempty"`
}

type Directory struct {
	People   []Person     `yaml:"people" json:"people"`
	Teams    []Team       `yaml:"teams" json:"teams"`
	Defaults []Preference `yaml:"defaultPreferences,omitempty" json:"defaultPreferences,omitempty"`
	Throttle Throttle     `yaml:"throttle,omitempty" json:"throttle,omitempty"`

	people   map[string]Person
	teams    map[string]*Team
	byPhone  map[string]string // normalized phone -> person id
	defaults []Preference
}

// Load reads teams.yaml. A missing file yields an empty directory.
func Load(path string) (*Directory, error) {
	d, err := readFile(path)
	if err != nil {
		return nil, err
	}
	return d, d.index()
}

func readFile(path string) (*Directory, error) {
	d := &Directory{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := yaml.Unmarshal(data, d); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return d, nil
}

func (d *Directory) index() error {
	d.people = map[string]Person{}
	d.teams = map[string]*Team{}
	d.byPhone = map[string]string{}
	for i := range d.People {
		p := &d.People[i]
		if p.ID == "" {
			return fmt.Errorf("people[%d]: id is required", i)
		}
		tz := p.Timezone
		if tz == "" {
			tz = "UTC"
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return fmt.Errorf("person %s: timezone %q: %w", p.ID, tz, err)
		}
		p.loc = loc
		if p.Preferences, err = compilePreferences(p.Preferences, "person "+p.ID); err != nil {
			return err
		}
		d.people[p.ID] = *p
		if n := NormalizePhone(p.Phone); n != "" {
			d.byPhone[n] = p.ID
		}
	}
	// Sort before taking pointers into the slice: sorting afterwards would
	// leave each map entry pointing at a different team.
	sort.Slice(d.Teams, func(i, j int) bool { return d.Teams[i].ID < d.Teams[j].ID })
	for i := range d.Teams {
		t := &d.Teams[i]
		for _, m := range t.Members {
			if _, ok := d.people[m]; !ok {
				return fmt.Errorf("team %s: unknown member %q", t.ID, m)
			}
		}
		var err error
		if t.Preferences, err = compilePreferences(t.Preferences, "team "+t.ID); err != nil {
			return err
		}
		if err := d.compileOnCall(t); err != nil {
			return err
		}
		d.teams[t.ID] = t
	}
	defaults := d.Defaults
	if len(defaults) == 0 {
		defaults = DefaultPreferences
	}
	var err error
	if d.defaults, err = compilePreferences(defaults, "defaultPreferences"); err != nil {
		return err
	}
	return d.Throttle.compile()
}

// Resolve expands a routing target into people:
//
//	team:<id>    every member
//	oncall:<id>  whoever is on call for the team at `now`
//	user:<id>    one person
func (d *Directory) Resolve(target string, now time.Time) ([]Person, string) {
	kind, id, _ := strings.Cut(target, ":")
	switch kind {
	case "user":
		if p, ok := d.people[id]; ok {
			return []Person{p}, ""
		}
	case "team":
		if t, ok := d.teams[id]; ok {
			out := make([]Person, 0, len(t.Members))
			for _, m := range t.Members {
				out = append(out, d.people[m])
			}
			return out, ""
		}
	case "oncall":
		if oc, ok := d.WhoIsOnCall(id, now); ok {
			return []Person{oc.Person}, "on call: " + oc.Reason
		}
	}
	return nil, ""
}

// Exists reports whether a routing target names a known team or person.
func (d *Directory) Exists(target string) bool {
	kind, id, _ := strings.Cut(target, ":")
	switch kind {
	case "user":
		_, ok := d.people[id]
		return ok
	case "team", "oncall":
		_, ok := d.teams[id]
		return ok
	}
	return false
}

func (d *Directory) Person(id string) (Person, bool) { p, ok := d.people[id]; return p, ok }

func (d *Directory) Team(id string) (Team, bool) {
	t, ok := d.teams[id]
	if !ok {
		return Team{}, false
	}
	return *t, true
}

// PersonByPhone finds who sent an inbound SMS or answered a call.
func (d *Directory) PersonByPhone(phone string) (Person, bool) {
	id, ok := d.byPhone[NormalizePhone(phone)]
	if !ok {
		return Person{}, false
	}
	return d.people[id], true
}

// NormalizePhone keeps a leading + and digits: "+44 7700 900101" -> "+447700900101".
func NormalizePhone(s string) string {
	var b strings.Builder
	for i, r := range strings.TrimSpace(s) {
		if (r >= '0' && r <= '9') || (r == '+' && i == 0) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Address returns how to reach a person on a channel ("" if they can't be).
func (p Person) Address(channel string) string {
	switch channel {
	case "email":
		return p.Email
	case "sms", "phone":
		return p.Phone
	}
	return ""
}

// Find looks a person up by id or name (case-insensitive), as typed in the
// UI's "acting as" box. Approvals use it to tell people apart.
func (d *Directory) Find(nameOrID string) (Person, bool) {
	s := strings.TrimSpace(nameOrID)
	if p, ok := d.people[s]; ok {
		return p, true
	}
	for _, p := range d.People {
		if strings.EqualFold(p.ID, s) || strings.EqualFold(p.Name, s) {
			return p, true
		}
	}
	return Person{}, false
}

// IsMember reports whether a person belongs to a team.
func (d *Directory) IsMember(teamID, personID string) bool {
	t, ok := d.Team(teamID)
	if !ok {
		return false
	}
	for _, m := range t.Members {
		if m == personID {
			return true
		}
	}
	return false
}
