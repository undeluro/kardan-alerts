package directory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const teamsYAML = `
people:
  - id: anna
    name: Anna
    phone: "+44 7700 900101"
    email: anna@example.com
    timezone: Europe/London
    preferences:
      - { severity: critical, channels: [sms], between: "08:00-20:00" }
      - { severity: critical, channels: [email] }
      - { severity: warning,  channels: [email] }
  - { id: sam,   name: Sam,   phone: "+44 7700 900102", timezone: Europe/London }
  - { id: maria, name: Maria, phone: "+44 7700 900103", timezone: Europe/London }
  - { id: priya, name: Priya, phone: "+1 212 555 0104", timezone: America/New_York }
  - { id: lee,   name: Lee,   phone: "+852 5550 0105",  timezone: Asia/Hong_Kong }
teams:
  - id: settlements
    members: [sam, maria]
    onCall:
      timezone: Europe/London
      rotation: { every: week, start: "2026-11-02T09:00", people: [sam, maria] }
      overrides:
        - { person: maria, from: "2026-12-24T00:00:00Z", to: "2026-12-27T00:00:00Z" }
  - id: global-ops
    members: [sam, priya, lee]
    preferences:
      - { severity: error, channels: [phone, sms] }
    onCall:
      shifts:
        - { person: lee,   between: "08:00-16:00" }
        - { person: sam,   between: "08:00-16:00" }
        - { person: priya, between: "08:00-16:00" }
`

func load(t *testing.T) *Directory {
	t.Helper()
	p := filepath.Join(t.TempDir(), "teams.yaml")
	if err := os.WriteFile(p, []byte(teamsYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestAnnaPreferences(t *testing.T) {
	d := load(t)
	anna, _ := d.Person("anna")
	cases := []struct {
		when, severity, want string
	}{
		{"2026-11-20T09:00:00Z", "critical", "sms"},   // 09:00 London
		{"2026-11-20T19:59:00Z", "critical", "sms"},   // 19:59 London
		{"2026-11-20T20:00:00Z", "critical", "email"}, // 20:00 London: outside the SMS window
		{"2026-07-20T19:30:00Z", "critical", "email"}, // 20:30 BST in summer
		{"2026-11-20T09:00:00Z", "error", "email"},    // error >= warning
		{"2026-11-20T09:00:00Z", "info", "ui"},        // falls through to defaults
	}
	for _, c := range cases {
		got, why := d.ChannelsFor(anna, "", c.severity, at(c.when))
		if strings.Join(got, ",") != c.want {
			t.Errorf("%s %s: got %v (%s), want %s", c.when, c.severity, got, why, c.want)
		}
	}
}

func TestTeamAndDefaultPreferences(t *testing.T) {
	d := load(t)
	sam, _ := d.Person("sam")
	got, _ := d.ChannelsFor(sam, "global-ops", "error", at("2026-11-20T09:00:00Z"))
	if strings.Join(got, ",") != "phone,sms" {
		t.Errorf("team preference: %v", got)
	}
	got, _ = d.ChannelsFor(sam, "settlements", "critical", at("2026-11-20T09:00:00Z"))
	if strings.Join(got, ",") != "phone,sms" {
		t.Errorf("default preference for critical: %v", got)
	}
}

func TestRotationAndOverride(t *testing.T) {
	d := load(t)
	cases := map[string]string{
		"2026-11-03T12:00:00Z": "sam",   // week 1
		"2026-11-09T08:59:00Z": "sam",   // just before the Monday 09:00 hand-over
		"2026-11-09T09:01:00Z": "maria", // week 2
		"2026-11-16T10:00:00Z": "sam",   // week 3
		"2026-12-25T10:00:00Z": "maria", // holiday override
	}
	for when, want := range cases {
		oc, ok := d.WhoIsOnCall("settlements", at(when))
		if !ok || oc.Person.ID != want {
			t.Errorf("%s: on call %s (%s), want %s", when, oc.Person.ID, oc.Reason, want)
		}
	}
	people, why := d.Resolve("oncall:settlements", at("2026-11-09T09:01:00Z"))
	if len(people) != 1 || people[0].ID != "maria" || !strings.Contains(why, "rotation") {
		t.Errorf("resolve oncall: %v %s", people, why)
	}
}

func TestFollowTheSun(t *testing.T) {
	d := load(t)
	cases := map[string]string{
		"2026-11-20T02:00:00Z": "lee",   // 10:00 Hong Kong
		"2026-11-20T10:00:00Z": "sam",   // 10:00 London
		"2026-11-20T15:00:00Z": "sam",   // London and New York overlap: the first listed shift wins
		"2026-11-20T17:00:00Z": "priya", // 12:00 New York, London shift over
	}
	for when, want := range cases {
		oc, _ := d.WhoIsOnCall("global-ops", at(when))
		if oc.Person.ID != want {
			t.Errorf("%s: %s (%s), want %s", when, oc.Person.ID, oc.Reason, want)
		}
	}
}

func TestWindowsAndValidation(t *testing.T) {
	w, _ := parseWindow(Window{Between: "22:00-06:00", Days: "Mon-Fri"})
	lon, _ := time.LoadLocation("Europe/London")
	if !w.contains(time.Date(2026, 11, 20, 23, 0, 0, 0, lon)) { // Friday 23:00
		t.Error("wrapping window should include Friday 23:00")
	}
	if !w.contains(time.Date(2026, 11, 21, 3, 0, 0, 0, lon)) { // Saturday 03:00, started Friday
		t.Error("wrapping window that began Friday should include Saturday 03:00")
	}
	if w.contains(time.Date(2026, 11, 21, 23, 0, 0, 0, lon)) { // Saturday 23:00
		t.Error("Saturday evening is outside Mon-Fri")
	}
	bad := []string{
		"people: [{id: a, name: A, timezone: Mars/Base}]",
		"people: [{id: a, name: A, preferences: [{severity: huge, channels: [sms]}]}]",
		"people: [{id: a, name: A, preferences: [{channels: [pigeon]}]}]",
		"people: [{id: a, name: A}]\nteams: [{id: t, members: [a], onCall: {rotation: {every: month, start: '2026-01-01T09:00', people: [a]}}}]",
		"people: [{id: a, name: A, preferences: [{channels: [sms], between: '8-20'}]}]",
	}
	for _, y := range bad {
		p := filepath.Join(t.TempDir(), "teams.yaml")
		_ = os.WriteFile(p, []byte(y), 0o644)
		if _, err := Load(p); err == nil {
			t.Errorf("expected error for:\n%s", y)
		}
	}
	d := load(t)
	if p, ok := d.PersonByPhone("+447700900101"); !ok || p.ID != "anna" {
		t.Error("phone lookup should ignore spaces")
	}
}
