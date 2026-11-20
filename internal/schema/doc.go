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
}

// Doc is one event as seen by the rule engine.
type Doc struct {
	Env    *event.Envelope
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
	default:
		r = d.firstScalar(facetCandidates[name])
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
	return []string{"$severity", "$service", "$type", "$env", "$source", "$id", "$receivedAt"}
}
