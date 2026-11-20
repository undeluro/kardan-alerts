package calendar

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func load(t *testing.T) *Set {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	s, err := Load(filepath.Join(filepath.Dir(file), "..", "..", "config", "calendars.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestOpenAndBusinessDays(t *testing.T) {
	s := load(t)
	xlon, _ := s.Get("XLON")
	cases := []struct {
		when       string
		open, bday bool
	}{
		{"2026-11-20T09:00:00Z", true, true},   // Friday 09:00 London
		{"2026-11-20T18:30:00Z", false, true},  // after hours
		{"2026-11-21T10:00:00Z", false, false}, // Saturday
		{"2026-12-28T10:00:00Z", false, false}, // Boxing Day substitute
		{"2026-07-20T06:30:00Z", true, true},   // 07:30 BST
	}
	for _, c := range cases {
		if got := xlon.IsOpen(at(c.when)); got != c.open {
			t.Errorf("%s open = %v, want %v", c.when, got, c.open)
		}
		if got := xlon.IsBusinessDay(at(c.when)); got != c.bday {
			t.Errorf("%s business day = %v, want %v", c.when, got, c.bday)
		}
	}
}

func TestNextCutoff(t *testing.T) {
	s := load(t)
	// Friday 15:00 Berlin: EUR customer cut-off at 17:00 today.
	d, ok := s.NextCutoff("eur", at("2026-11-20T14:00:00Z"))
	if !ok || d.Name != "T2 customer payments" || !d.At.Equal(at("2026-11-20T16:00:00Z")) {
		t.Errorf("EUR = %+v", d)
	}
	// Friday 17:30 Berlin: customer cut-off passed, interbank 18:00 still today.
	d, _ = s.NextCutoff("EUR", at("2026-11-20T16:30:00Z"))
	if d.Name != "T2 interbank payments" {
		t.Errorf("after customer cut-off = %+v", d)
	}
	// Friday 19:00 Berlin: next business day is Monday.
	d, _ = s.NextCutoff("EUR", at("2026-11-20T18:30:00Z"))
	if !d.At.Equal(at("2026-11-23T16:00:00Z")) {
		t.Errorf("weekend roll = %+v", d)
	}
	// Thanksgiving: Fedwire closed, roll to Friday.
	d, _ = s.NextCutoff("USD", at("2026-11-26T15:00:00Z"))
	if d.At.In(time.UTC).Day() != 27 {
		t.Errorf("holiday roll = %+v", d)
	}
	if _, ok := s.NextCutoff("XYZ", at("2026-11-20T14:00:00Z")); ok {
		t.Error("unknown currency has no cut-off")
	}
}

func TestStatus(t *testing.T) {
	st := load(t).Status(at("2026-11-20T14:00:00Z"))
	if len(st) < 5 {
		t.Fatalf("status = %d calendars", len(st))
	}
	for _, c := range st {
		if c.ID == "T2" && (!c.Open || len(c.Cutoffs) != 2 || c.Cutoffs[0].Name != "T2 customer payments") {
			t.Errorf("T2 status = %+v", c)
		}
	}
}
