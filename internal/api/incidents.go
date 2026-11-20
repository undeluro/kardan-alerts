package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/calendar"
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

// getCalendars shows market and payment-system calendars at business time:
// open or closed, holiday, and the next cut-offs with time left.
func (s *Server) getCalendars(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	if s.Clock != nil {
		now = s.Clock.Now()
	}
	var set *calendar.Set
	if s.Calendars != nil {
		set = s.Calendars()
	}
	note := ""
	if set != nil {
		note = set.Note
	}
	st := set.Status(now)
	if st == nil {
		st = []calendar.Status{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "note": note, "calendars": st})
}
