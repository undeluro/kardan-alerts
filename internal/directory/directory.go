// Package directory knows people, teams and how to reach them.
package directory

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

type Person struct {
	ID       string `yaml:"id" json:"id"`
	Name     string `yaml:"name" json:"name"`
	Email    string `yaml:"email,omitempty" json:"email,omitempty"`
	Phone    string `yaml:"phone,omitempty" json:"phone,omitempty"`
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"`
}

type Team struct {
	ID      string   `yaml:"id" json:"id"`
	Name    string   `yaml:"name" json:"name"`
	Members []string `yaml:"members" json:"members"`
}

type Directory struct {
	People []Person `yaml:"people" json:"people"`
	Teams  []Team   `yaml:"teams" json:"teams"`

	people map[string]Person
	teams  map[string]Team
}

// Load reads teams.yaml. A missing file yields an empty directory.
func Load(path string) (*Directory, error) {
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
	return d, d.index()
}

func (d *Directory) index() error {
	d.people = map[string]Person{}
	d.teams = map[string]Team{}
	for _, p := range d.People {
		d.people[p.ID] = p
	}
	for _, t := range d.Teams {
		for _, m := range t.Members {
			if _, ok := d.people[m]; !ok {
				return fmt.Errorf("team %s: unknown member %q", t.ID, m)
			}
		}
		d.teams[t.ID] = t
	}
	sort.Slice(d.Teams, func(i, j int) bool { return d.Teams[i].ID < d.Teams[j].ID })
	return nil
}

// Resolve expands "team:<id>" or "user:<id>" into people.
func (d *Directory) Resolve(target string) []Person {
	kind, id, _ := strings.Cut(target, ":")
	switch kind {
	case "user":
		if p, ok := d.people[id]; ok {
			return []Person{p}
		}
	case "team":
		if t, ok := d.teams[id]; ok {
			out := make([]Person, 0, len(t.Members))
			for _, m := range t.Members {
				out = append(out, d.people[m])
			}
			return out
		}
	}
	return nil
}

func (d *Directory) Person(id string) (Person, bool) { p, ok := d.people[id]; return p, ok }
func (d *Directory) Team(id string) (Team, bool)     { t, ok := d.teams[id]; return t, ok }

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
