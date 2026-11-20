package api

import (
	"errors"
	"net/http"

	"github.com/undeluro/kardan-alerts/internal/alerting"
)

func (s *Server) minAlerts() int {
	if s.Correlation != nil {
		return s.Correlation().MinAlerts
	}
	return 2
}

// listIncidents: GET /api/incidents?status=open
func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Incidents(r.Context(), r.URL.Query().Get("status"), s.minAlerts())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// getIncident returns an incident with its alerts, most impactful first.
func (s *Server) getIncident(w http.ResponseWriter, r *http.Request) {
	inc, err := s.Store.Incident(r.Context(), r.PathValue("id"))
	if errors.Is(err, alerting.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	alerts, err := s.Store.List(r.Context(), alerting.ListFilter{Incident: inc.ID, Sort: "impact", Limit: 200})
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, a := range alerts {
		a.Sample, a.SampleRaw = nil, ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"incident": inc, "alerts": alerts})
}

// getFX shows the rates used to normalise money at risk.
func (s *Server) getFX(w http.ResponseWriter, _ *http.Request) {
	if s.FX == nil || s.FX() == nil {
		writeJSON(w, http.StatusOK, map[string]any{"usdPer": map[string]float64{"USD": 1}})
		return
	}
	writeJSON(w, http.StatusOK, s.FX())
}
