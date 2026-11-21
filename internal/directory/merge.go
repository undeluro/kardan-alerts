package directory

import (
	"encoding/json"
	"fmt"
)

// Origin of a person or team: "file" (teams.yaml), "api" (created in the UI)
// or "override" (a UI edit of a file entry).
type Origins map[string]string // "person:<id>" / "team:<id>" -> origin

// LoadMerged reads teams.yaml and applies stored entries. An entry that would
// make the directory invalid (bad timezone, unknown member...) is skipped and
// reported, so one bad edit cannot take routing down for everyone.
func LoadMerged(path string, entries []Entry) (*Directory, Origins, []string, error) {
	base, err := readFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := clone(base).index(); err != nil {
		return nil, nil, nil, err
	}
	origins := Origins{}
	for _, p := range base.People {
		origins[KindPerson+":"+p.ID] = "file"
	}
	for _, t := range base.Teams {
		origins[KindTeam+":"+t.ID] = "file"
	}
	cur := base
	var problems []string
	// People first: teams refer to them.
	for _, kind := range []string{KindPerson, KindTeam} {
		for _, e := range entries {
			if e.Kind != kind {
				continue
			}
			next, err := Apply(cur, e)
			if err == nil {
				err = clone(next).index()
			}
			if err != nil {
				problems = append(problems, fmt.Sprintf("stored %s %s skipped: %v", e.Kind, e.ID, err))
				continue
			}
			key := e.Kind + ":" + e.ID
			if origins[key] == "file" || origins[key] == "override" {
				origins[key] = "override"
			} else {
				origins[key] = "api"
			}
			cur = next
		}
	}
	d := clone(cur)
	return d, origins, problems, d.index()
}

// Apply returns a copy of d with the entry merged in (not indexed).
func Apply(d *Directory, e Entry) (*Directory, error) {
	out := clone(d)
	switch e.Kind {
	case KindPerson:
		var p Person
		if err := json.Unmarshal(e.Body, &p); err != nil {
			return nil, fmt.Errorf("person: %w", err)
		}
		p.ID = e.ID
		replaced := false
		for i := range out.People {
			if out.People[i].ID == p.ID {
				out.People[i], replaced = p, true
			}
		}
		if !replaced {
			out.People = append(out.People, p)
		}
	case KindTeam:
		var t Team
		if err := json.Unmarshal(e.Body, &t); err != nil {
			return nil, fmt.Errorf("team: %w", err)
		}
		t.ID = e.ID
		replaced := false
		for i := range out.Teams {
			if out.Teams[i].ID == t.ID {
				out.Teams[i], replaced = t, true
			}
		}
		if !replaced {
			out.Teams = append(out.Teams, t)
		}
	default:
		return nil, fmt.Errorf("unknown kind %q", e.Kind)
	}
	return out, nil
}

// Validate reports whether the directory is consistent (used before saving).
func (d *Directory) Validate() error { return clone(d).index() }

// clone copies the exported, user-provided parts of a directory. Indexing
// compiles preferences in place, so it always works on a copy.
func clone(d *Directory) *Directory {
	b, _ := json.Marshal(d)
	out := &Directory{}
	_ = json.Unmarshal(b, out)
	return out
}
