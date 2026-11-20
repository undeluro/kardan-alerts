package processor

import (
	"testing"
	"time"
)

func TestTightened(t *testing.T) {
	now := time.Date(2026, 11, 20, 22, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { c := now.Add(d); return &c }
	cases := []struct {
		cutoff *time.Time
		want   time.Duration
		note   string
	}{
		{nil, 10 * time.Minute, ""},
		{at(3 * time.Hour), 10 * time.Minute, ""},
		{at(30 * time.Minute), 5 * time.Minute, "30 min left to"},
		{at(5 * time.Minute), 2 * time.Minute, "5 min left to"}, // 20% floor
		{at(-time.Minute), 2 * time.Minute, "missed"},
	}
	for _, c := range cases {
		got, note := tightened(10*time.Minute, c.cutoff, now)
		if got != c.want || note != c.note {
			t.Errorf("cutoff %v: got %v %q, want %v %q", c.cutoff, got, note, c.want, c.note)
		}
	}
}

func TestShortDur(t *testing.T) {
	for d, want := range map[time.Duration]string{34100 * time.Millisecond: "34 s", 10 * time.Minute: "10 min",
		125 * time.Minute: "2 h 5 min", 2 * time.Hour: "2 h", 72 * time.Hour: "3 days"} {
		if got := shortDur(d); got != want {
			t.Errorf("shortDur(%v) = %q, want %q", d, got, want)
		}
	}
}
