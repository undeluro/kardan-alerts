// Package schema gives rules a uniform view over payloads of any shape.
//
// Plain paths ("error.code", "labels.severity", "payload.notional") read the
// raw payload with gjson, without unmarshalling it. Paths starting with "$"
// are canonical facets derived by heuristics, so one rule such as
// "$severity severityAtLeast critical" works across a Prometheus webhook, a
// workflow event and a business event that spell severity differently.
package schema

import (
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/undeluro/kardan-alerts/internal/event"
)

// facetCandidates lists where each canonical facet is usually found, in
// priority order. Per-schema overrides arrive with the schema registry.
var facetCandidates = map[string][]string{
	"$severity": {"severity", "level", "labels.severity", "log.level", "priority", "meta.severity",
		"payload.severity", "status.severity", "fields.severity"},
	"$service": {"service", "labels.service", "service.name", "meta.producer", "producer", "app",
		"application", "labels.job", "component", "host"},
	"$type": {"type", "alertname", "meta.schema", "eventType", "event_type", "event", "kind", "schema",
		"meta.type"},
	"$env": {"environment", "env", "labels.environment", "labels.env", "meta.env", "meta.environment"},

	// Finance facets. Schemas with unusual field names get these mapped
	// automatically by field detection (detect.go) or by hand (schema facets).
	"$instrument":   {"instrument", "symbol", "Symbol", "isin", "payload.instrument", "payload.isin", "payload.symbol", "ticker", "SecurityID"},
	"$counterparty": {"counterparty", "cpty", "payload.counterparty", "payload.cpty", "counterpartyLei", "PartyID"},
	"$currency":     {"currency", "ccy", "Currency", "payload.currency", "payload.ccy", "amount.currency"},
	"$notional":     {"notional", "payload.notional", "amount", "payload.amount", "notionalAmount", "amount.value"},
	"$tradeId":      {"tradeId", "payload.tradeId", "TradeID", "ClOrdID", "orderId", "endToEndId", "workflowId"},
	"$desk":         {"desk", "payload.desk", "book", "payload.book"},
	"$account":      {"account", "iban", "debtorIban", "Account"},
}

// Converter turns an amount in some currency into USD (see finance.FX).
type Converter interface {
	ToUSD(amount float64, ccy string) (float64, bool)
}

// Doc is one event as seen by the rule engine.
type Doc struct {
	Env *event.Envelope
	// Overrides maps facets to paths for this event's schema (auto-detected
	// or set by a person); they take precedence over the generic candidates.
	Overrides map[string]string
	// FX enables the $notionalUsd facet.
	FX     Converter
	facets map[string]gjson.Result
}

func NewDoc(env *event.Envelope) *Doc { return &Doc{Env: env} }

// Get resolves a payload path or a "$facet".
func (d *Doc) Get(path string) gjson.Result {
	if len(path) > 0 && path[0] == '$' {
		return d.facet(path)
	}
	return gjson.GetBytes(d.Env.Payload, path)
}

func (d *Doc) facet(name string) gjson.Result {
	if r, ok := d.facets[name]; ok {
		return r
	}
	var r gjson.Result
	switch name {
	case "$source":
		r = str(d.Env.SourceID)
	case "$id":
		r = str(d.Env.ID)
	case "$receivedAt":
		r = str(d.Env.ReceivedAt.UTC().Format(time.RFC3339Nano))
	case "$notionalUsd":
		r = d.notionalUSD()
	case "$raw":
		r = str(d.Env.Raw)
	case "$format":
		r = str(d.Env.Format)
	default:
		if p, ok := d.Overrides[name]; ok && p != "" {
			r = gjson.GetBytes(d.Env.Payload, p)
		}
		if !r.Exists() {
			r = d.firstScalar(facetCandidates[name])
		}
		if name == "$severity" && r.Exists() {
			if norm := NormalizeSeverity(r.String()); norm != "" {
				r = str(norm)
			}
		}
	}
	if d.facets == nil {
		d.facets = make(map[string]gjson.Result, 4)
	}
	d.facets[name] = r
	return r
}

// notionalUSD converts $notional from $currency (USD when no currency is
// given) using the FX table.
func (d *Doc) notionalUSD() gjson.Result {
	n := d.Get("$notional")
	var amount float64
	switch n.Type {
	case gjson.Number:
		amount = n.Num
	case gjson.String:
		f, err := strconv.ParseFloat(strings.TrimSpace(n.Str), 64)
		if err != nil {
			return gjson.Result{}
		}
		amount = f
	default:
		return gjson.Result{}
	}
	ccy := d.Get("$currency").String()
	if ccy == "" {
		ccy = "USD"
	}
	if d.FX == nil {
		if strings.EqualFold(ccy, "USD") {
			return num(amount)
		}
		return gjson.Result{}
	}
	usd, ok := d.FX.ToUSD(amount, ccy)
	if !ok {
		return gjson.Result{}
	}
	return num(usd)
}

func num(f float64) gjson.Result {
	raw := strconv.FormatFloat(f, 'f', -1, 64)
	return gjson.Result{Type: gjson.Number, Num: f, Raw: raw}
}

func (d *Doc) firstScalar(paths []string) gjson.Result {
	for _, p := range paths {
		r := gjson.GetBytes(d.Env.Payload, p)
		if (r.Type == gjson.String && r.Str != "") || r.Type == gjson.Number {
			return r
		}
	}
	return gjson.Result{}
}

// str builds a synthetic gjson string result.
func str(s string) gjson.Result {
	if s == "" {
		return gjson.Result{}
	}
	return gjson.Result{Type: gjson.String, Str: s, Raw: strconv.Quote(s)}
}

// FacetNames lists the canonical facets rules may reference.
func FacetNames() []string {
	return []string{"$severity", "$service", "$type", "$env", "$instrument", "$counterparty", "$currency",
		"$notional", "$notionalUsd", "$tradeId", "$desk", "$account", "$source", "$format", "$raw", "$id", "$receivedAt"}
}

// IsFacet reports whether name is a known canonical facet.
func IsFacet(name string) bool {
	for _, f := range FacetNames() {
		if f == name {
			return true
		}
	}
	return false
}
