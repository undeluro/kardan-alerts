package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/undeluro/kardan-alerts/internal/schema"
)

// inspectEvent parses a pasted event the way ingest would and lists its
// fields with detected finance kinds and canonical facets, without storing
// anything. The onboarding wizard builds its first rule from this.
// POST /api/events/inspect {"raw": "...", "source": "optional-source-id"}
func (s *Server) inspectEvent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Raw    string `json:"raw"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Source == "" {
		req.Source = "onboarding"
	}
	env := envelopeFromRaw(s, req.Source, []byte(req.Raw))
	doc := schema.NewDoc(env)
	doc.FX = s.fxTable()
	sig := schema.Signature(env.Payload)
	if s.Schemas != nil {
		sig, doc.Overrides, _ = s.Schemas.Identify(env.Payload)
	}
	fields := schema.Flatten(env.Payload)
	facets := map[string]any{}
	for _, f := range schema.FacetNames() {
		switch f {
		case "$id", "$receivedAt", "$raw":
			continue
		}
		if v := doc.Get(f); v.Exists() {
			facets[f] = v.Value()
		}
	}
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Detected != "" && fields[j].Detected == "" })
	writeJSON(w, http.StatusOK, map[string]any{
		"format": env.Format, "signature": sig, "payload": json.RawMessage(env.Payload),
		"fields": fields, "facets": facets,
	})
}
