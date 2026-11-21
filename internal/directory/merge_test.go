package directory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const mergeYAML = `
people:
  - { id: anna, name: Anna, timezone: Europe/London }
  - { id: sam, name: Sam }
teams:
  - { id: pay, name: Payments, members: [anna] }
`

func entry(kind, id string, v any) Entry {
	b, _ := json.Marshal(v)
	return Entry{Kind: kind, ID: id, Body: b}
}

func TestLoadMerged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "teams.yaml")
	if err := os.WriteFile(path, []byte(mergeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{
		// A team edit that adds a person created in the UI (people apply first).
		entry(KindTeam, "pay", map[string]any{"name": "Payments Platform", "members": []string{"anna", "zoe"},
			"preferences": []map[string]any{{"severity": "critical", "channels": []string{"sms"}, "between": "08:00-20:00"}}}),
		entry(KindPerson, "zoe", map[string]any{"name": "Zoe", "timezone": "America/New_York"}),
		entry(KindTeam, "fx", map[string]any{"name": "FX Ops", "members": []string{"nobody"}}), // invalid: skipped
		entry(KindPerson, "sam", map[string]any{"name": "Sam", "timezone": "Mars/Base"}),       // invalid: skipped
	}
	d, origins, problems, err := LoadMerged(path, entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 2 {
		t.Fatalf("want 2 problems, got %v", problems)
	}
	team, ok := d.Team("pay")
	if !ok || team.Name != "Payments Platform" || len(team.Members) != 2 {
		t.Fatalf("team override not applied: %+v", team)
	}
	if _, ok := d.Team("fx"); ok {
		t.Fatal("invalid team should be skipped")
	}
	if p, _ := d.Person("sam"); p.Timezone != "" {
		t.Fatalf("invalid person edit should keep the file version, got %+v", p)
	}
	want := map[string]string{"team:pay": "override", "person:zoe": "api", "person:anna": "file", "person:sam": "file"}
	for k, v := range want {
		if origins[k] != v {
			t.Errorf("origin %s = %q, want %q", k, origins[k], v)
		}
	}
	// The merged directory routes: team members resolve, preferences compiled.
	people, _ := d.Resolve("team:pay", time.Now())
	if len(people) != 2 {
		t.Fatalf("resolve team:pay = %d people", len(people))
	}
}
