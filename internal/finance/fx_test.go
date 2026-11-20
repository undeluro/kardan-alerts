package finance

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

func loadFX(t *testing.T) *FX {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fx.yaml")
	_ = os.WriteFile(p, []byte("usdPer: { EUR: 1.08, GBP: 1.27, JPY: 0.0067 }\n"), 0o644)
	fx, err := LoadFX(p)
	if err != nil {
		t.Fatal(err)
	}
	return fx
}

func TestNotionalUSDFacet(t *testing.T) {
	fx := loadFX(t)
	cases := map[string]float64{
		`{"payload":{"notional":25000000,"currency":"EUR"}}`: 27_000_000,
		`{"amount":{"value":"4000000000","currency":"JPY"}}`: 26_800_000,
		`{"notional":5000000}`:                               5_000_000, // no currency -> USD
	}
	for payload, want := range cases {
		d := schema.NewDoc(&event.Envelope{Payload: json.RawMessage(payload)})
		d.FX = fx
		got := d.Get("$notionalUsd")
		if !got.Exists() || math.Abs(got.Num-want) > 1 {
			t.Errorf("%s: $notionalUsd = %v, want %v", payload, got, want)
		}
	}
	d := schema.NewDoc(&event.Envelope{Payload: json.RawMessage(`{"notional":1,"currency":"XYZ"}`)})
	d.FX = fx
	if d.Get("$notionalUsd").Exists() {
		t.Error("unknown currency must not produce a USD value")
	}
	if _, err := LoadFX(filepath.Join(t.TempDir(), "missing.yaml")); err != nil {
		t.Errorf("missing fx.yaml should fall back to USD only: %v", err)
	}
}

func TestImpactOrdersBySeverityAndMoney(t *testing.T) {
	small := Impact("warning", 1_000_000, true)
	big := Impact("warning", 25_000_000, true)
	crit := Impact("critical", 0, false)
	critBig := Impact("critical", 1_000_000_000, true)
	if !(small < big && big < crit+40 && crit < critBig && critBig <= 100) {
		t.Errorf("impact ordering: small=%d big=%d crit=%d critBig=%d", small, big, crit, critBig)
	}
	if Impact("info", 0, false) != 10 || Impact("whatever", 0, false) != 10 {
		t.Error("unknown/info severity base should be 10")
	}
}

func TestCorrelationKey(t *testing.T) {
	c, err := LoadCorrelation(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	d := schema.NewDoc(&event.Envelope{Payload: json.RawMessage(`{"counterparty":"ACME","symbol":"EURUSD","service":"x"}`)})
	if f, v := c.Key(d); f != "$counterparty" || v != "ACME" {
		t.Errorf("key = %s=%s", f, v)
	}
	d = schema.NewDoc(&event.Envelope{Payload: json.RawMessage(`{"symbol":"EURUSD"}`)})
	if f, v := c.Key(d); f != "$instrument" || v != "EURUSD" {
		t.Errorf("key = %s=%s", f, v)
	}
	p := filepath.Join(t.TempDir(), "c.yaml")
	_ = os.WriteFile(p, []byte("window: soon\n"), 0o644)
	if _, err := LoadCorrelation(p); err == nil {
		t.Error("bad window should fail")
	}
}

func TestUrgency(t *testing.T) {
	cut := time.Date(2026, 11, 20, 16, 0, 0, 0, time.UTC)
	cases := map[time.Duration]int{3 * time.Hour: 0, 2 * time.Hour: 0, time.Hour: 10, 30 * time.Minute: 15, 0: 20, -time.Hour: 20}
	for before, want := range cases {
		if got := Urgency(cut, cut.Add(-before)); got != want {
			t.Errorf("%v before cut-off: urgency %d, want %d", before, got, want)
		}
	}
	if Urgency(time.Time{}, cut) != 0 {
		t.Error("no cut-off, no urgency")
	}
}
