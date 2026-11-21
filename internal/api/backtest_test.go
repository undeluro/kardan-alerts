package api

import (
	"testing"
	"time"

	"github.com/undeluro/kardan-alerts/internal/rules"
)

func TestSimulateAggregate(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	spec := rules.AggregateSpec{Window: 10 * time.Minute, Threshold: 3}
	hits := []matchedEvent{
		// Counterparty A: 3 in 4 minutes -> fires once; the 4th stays in the same crossing.
		{at: at(0), fp: "A", value: 1}, {at: at(2), fp: "A", value: 1}, {at: at(4), fp: "A", value: 1}, {at: at(5), fp: "A", value: 1},
		// Window drains, then crosses again -> second alert.
		{at: at(40), fp: "A", value: 1}, {at: at(41), fp: "A", value: 1}, {at: at(42), fp: "A", value: 1},
		// Counterparty B: 3 events spread over 30 minutes never reach 3 within 10.
		{at: at(0), fp: "B", value: 1}, {at: at(15), fp: "B", value: 1}, {at: at(30), fp: "B", value: 1},
	}
	if got := simulateAggregate(hits, spec); got != 2 {
		t.Fatalf("fires = %d, want 2", got)
	}
	// Sums: 30M + 25M crosses a 50M threshold.
	sum := []matchedEvent{{at: at(0), fp: "C", value: 30e6}, {at: at(1), fp: "C", value: 25e6}}
	if got := simulateAggregate(sum, rules.AggregateSpec{Window: time.Hour, Threshold: 50e6}); got != 1 {
		t.Fatalf("sum fires = %d, want 1", got)
	}
}
