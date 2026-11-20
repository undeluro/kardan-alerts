package schema

import (
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"github.com/undeluro/kardan-alerts/internal/event"
)

func TestIdentifierChecksums(t *testing.T) {
	valid := map[string]func(string) bool{
		"US0378331005":                     IsISIN, // Apple
		"GB0002634946":                     IsISIN, // BAE Systems
		"DE000BAY0017":                     IsISIN, // Bayer
		"GB82WEST12345698765432":           IsIBAN,
		"DE89370400440532013000":           IsIBAN,
		withLEICheck("5493001KJTIIGC8Y1R"): IsLEI,
	}
	for v, fn := range valid {
		if !fn(v) {
			t.Errorf("%s should be valid", v)
		}
	}
	invalid := map[string]func(string) bool{
		"US0378331006":           IsISIN, // bad check digit
		"GB82WEST12345698765433": IsIBAN,
		"SETTLEMENTS":            IsISIN,
		"5493001KJTIIGC8Y1R00":   IsLEI,
	}
	for v, fn := range invalid {
		if fn(v) {
			t.Errorf("%s should be invalid", v)
		}
	}
}

// withLEICheck appends ISO 7064 mod 97-10 check digits to an 18-char base.
func withLEICheck(base string) string {
	var digits string
	for _, r := range base + "00" {
		if r >= 'A' && r <= 'Z' {
			digits += fmt.Sprint(int(r-'A') + 10)
		} else {
			digits += string(r)
		}
	}
	n, _ := new(big.Int).SetString(digits, 10)
	check := 98 - new(big.Int).Mod(n, big.NewInt(97)).Int64()
	return fmt.Sprintf("%s%02d", base, check)
}

func TestFlattenDetectsFinanceFields(t *testing.T) {
	payload := `{
	  "evt": "SETTLEMENT_FAIL",
	  "instr": {"id": {"isin": "US0378331005"}},
	  "cpty": {"lei": "` + withLEICheck("5493001KJTIIGC8Y1R") + `", "name": "Acme"},
	  "amt": {"value": 1250000.5, "ccy": "USD"},
	  "settleDate": "2026-11-21",
	  "dealId": "D-77812",
	  "client": {"name": "Jane Doe", "acct": {"iban": "GB82WEST12345698765432"}},
	  "tags": ["fx", "t+1"],
	  "legs": [{"qty": 10, "px": 99.5}, {"qty": 20, "px": 99.7}]
	}`
	fields := Flatten([]byte(payload))
	got := map[string]FieldInfo{}
	for _, f := range fields {
		got[f.Path] = f
	}
	want := map[string]string{
		"instr.id.isin":    KindISIN,
		"cpty.lei":         KindLEI,
		"amt.value":        KindAmount,
		"amt.ccy":          KindCurrency,
		"settleDate":       KindSettlementDate,
		"dealId":           KindReference,
		"client.name":      KindPartyName,
		"client.acct.iban": KindIBAN,
		"legs.0.qty":       KindQuantity,
		"legs.0.px":        KindPrice,
	}
	for path, kind := range want {
		if got[path].Detected != kind {
			t.Errorf("%s detected %q, want %q", path, got[path].Detected, kind)
		}
	}
	if !got["client.acct.iban"].Sensitive || !got["client.name"].Sensitive {
		t.Error("IBAN and client name must be flagged sensitive")
	}
	if got["tags"].Type != "array" {
		t.Errorf("tags type = %q", got["tags"].Type)
	}

	facets := AutoFacets(fields)
	expect := map[string]string{"$instrument": "instr.id.isin", "$counterparty": "cpty.lei", "$notional": "amt.value",
		"$currency": "amt.ccy", "$tradeId": "dealId", "$account": "client.acct.iban"}
	for f, p := range expect {
		if facets[f] != p {
			t.Errorf("auto facet %s = %q, want %q", f, facets[f], p)
		}
	}

	// The facets work end to end in a Doc.
	d := NewDoc(&event.Envelope{Payload: json.RawMessage(payload)})
	d.Overrides = facets
	if d.Get("$notional").Float() != 1250000.5 || d.Get("$instrument").String() != "US0378331005" {
		t.Errorf("facets via overrides: notional=%v instrument=%v", d.Get("$notional"), d.Get("$instrument"))
	}
}

func TestSignature(t *testing.T) {
	a := Signature([]byte(`{"type":"X","n":1,"err":{"code":"A"}}`))
	b := Signature([]byte(`{"err":{"code":"B"},"n":2,"type":"Y"}`))
	if a != b {
		t.Error("same structure with different values/order must share a signature")
	}
	c := Signature([]byte(`{"type":"X","n":"1","err":{"code":"A"}}`))
	if a == c {
		t.Error("a field changing type is a different schema")
	}
	p1 := Signature([]byte(`{"positions":{"T-10001":{"qty":1}}}`))
	p2 := Signature([]byte(`{"positions":{"T-20002":{"qty":5}}}`))
	if p1 != p2 {
		t.Error("id-like map keys should collapse")
	}
}

func TestManualOverrideWins(t *testing.T) {
	reg := NewRegistry(nil, nil)
	payload := []byte(`{"criticity":"HIGH","level":"info"}`)
	sig, ov, fields := reg.Identify(payload)
	if fields == nil {
		t.Fatal("first sighting should return fields")
	}
	if _, _, again := reg.Identify(payload); again != nil {
		t.Error("second sighting should be cached")
	}
	d := NewDoc(&event.Envelope{Payload: payload})
	d.Overrides = ov
	if d.Get("$severity").String() != "info" {
		t.Fatalf("before override: %s", d.Get("$severity"))
	}
	m := map[string]map[string]string{sig: {"$severity": "criticity"}}
	reg.manual.Store(&m)
	_, ov, _ = reg.Identify(payload)
	d = NewDoc(&event.Envelope{Payload: payload})
	d.Overrides = ov
	if d.Get("$severity").String() != "error" { // HIGH -> error
		t.Errorf("after override: %s", d.Get("$severity"))
	}
}

func TestAutoFacetsPreferShallowFields(t *testing.T) {
	fields := Flatten([]byte(`{"Document":{"Hdr":{"MsgId":"M-1"},"Tx":{"Amt":{"value":5}}},"endToEndId":"E2E-1","amount":5}`))
	f := AutoFacets(fields)
	if f["$tradeId"] != "endToEndId" || f["$notional"] != "amount" {
		t.Errorf("auto facets = %v", f)
	}
}
