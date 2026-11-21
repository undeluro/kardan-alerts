// Package api serves the REST API, the live feed and the embedded web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/undeluro/kardan-alerts/internal/actions"
	"github.com/undeluro/kardan-alerts/internal/ai"
	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/audit"
	"github.com/undeluro/kardan-alerts/internal/calendar"
	"github.com/undeluro/kardan-alerts/internal/directory"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/finance"
	"github.com/undeluro/kardan-alerts/internal/ingest"
	"github.com/undeluro/kardan-alerts/internal/platform/clock"
	"github.com/undeluro/kardan-alerts/internal/proposals"
	"github.com/undeluro/kardan-alerts/internal/replay"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/runbooks"
	"github.com/undeluro/kardan-alerts/internal/runner"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

type Server struct {
	Store          *alerting.Store
	Publisher      *alerting.Publisher
	Engine         *rules.Engine
	RuleStore      *rules.Store
	Schemas        *schema.Registry
	Directory      func() *directory.Directory
	Sources        func() []ingest.SourceStatus // nil when this replica has no ingest role
	ConfigErrors   func() []string
	Notify         func(ctx context.Context, kind string) error
	ParseRaw       func(source string, raw []byte) *event.Envelope
	DefaultWindow  time.Duration
	Clock          *clock.Clock
	GatewayToken   string
	ChannelStates  func(r *http.Request) map[string]string // breaker state per channel
	FX             func() *finance.FX
	Correlation    func() *finance.Correlation
	Calendars      func() *calendar.Set
	CheckRule      func(*rules.Compiled) error // reference checks (calendars, cut-offs)
	Runner         *runner.Runner
	Runbooks       *runbooks.Holder
	RunbookStore   *runbooks.Store
	Actions        func() *actions.Registry
	Audit          *audit.Log
	ApprovalSecret string
	Hub            *Hub
	// People and teams edited in the UI (nil: read-only, teams.yaml only).
	DirStore         *directory.Store
	DirectoryOrigins func() directory.Origins
	CheckDirectory   func(context.Context, directory.Entry) error
	ReloadDirectory  func(context.Context)
	Replay           *replay.Replayer // reads past events for rule backtests
	Proposals        *proposals.Store
	AI               *ai.Assistant
	UI               fs.FS // built React app; nil when not embedded
	Log              *slog.Logger
	Info             map[string]any
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("GET /api/alerts", s.listAlerts)
	mux.HandleFunc("GET /api/alerts/{id}", s.getAlert)
	mux.HandleFunc("POST /api/alerts/{id}/ack", s.transition("ack"))
	mux.HandleFunc("POST /api/alerts/{id}/resolve", s.transition("resolve"))
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("GET /api/teams", s.teams)
	mux.HandleFunc("GET /api/rules", s.listRules)
	mux.HandleFunc("POST /api/rules", s.createRule)
	mux.HandleFunc("POST /api/rules/test", s.testRule)
	mux.HandleFunc("POST /api/rules/backtest", s.backtestRule)
	mux.HandleFunc("POST /api/events/inspect", s.inspectEvent)
	mux.HandleFunc("GET /api/rules/{id}", s.getRule)
	mux.HandleFunc("PUT /api/rules/{id}", s.updateRule)
	mux.HandleFunc("DELETE /api/rules/{id}", s.deleteRule)
	mux.HandleFunc("GET /api/schemas", s.listSchemas)
	mux.HandleFunc("GET /api/schemas/{id}", s.getSchema)
	mux.HandleFunc("PUT /api/schemas/{id}", s.updateSchema)
	mux.HandleFunc("GET /api/sources", s.listSources)
	mux.HandleFunc("GET /api/incidents", s.listIncidents)
	mux.HandleFunc("GET /api/incidents/{id}", s.getIncident)
	mux.HandleFunc("GET /api/fx", s.getFX)
	mux.HandleFunc("GET /api/calendars", s.getCalendars)
	s.registerRunbooks(mux)
	s.registerDirectory(mux)
	s.registerAssist(mux)
	mux.HandleFunc("GET /api/deliveries", s.listDeliveries)
	mux.HandleFunc("POST /api/notifications/{id}/resend", s.resend)
	mux.HandleFunc("GET /api/channels", s.channels)
	mux.HandleFunc("GET /api/oncall", s.onCall)
	mux.HandleFunc("GET /api/clock", s.getClock)
	mux.HandleFunc("PUT /api/clock", s.setClock)
	mux.HandleFunc("POST /inbound/sms", s.gatewayAuth(s.inboundSMS))
	mux.HandleFunc("POST /inbound/call", s.gatewayAuth(s.inboundCall))
	mux.Handle("GET /api/stream", s.Hub)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "no such endpoint")
	})
	mux.Handle("/", s.spa())
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	out := map[string]any{"facets": schema.FacetNames(), "ops": rules.Ops, "configErrors": []string{}}
	if s.ConfigErrors != nil {
		if errs := s.ConfigErrors(); len(errs) > 0 {
			out["configErrors"] = errs
		}
	}
	for k, v := range s.Info {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	alerts, err := s.Store.List(r.Context(), alerting.ListFilter{Team: q.Get("team"), Status: q.Get("status"),
		Incident: q.Get("incident"), Sort: q.Get("sort"), Limit: limit})
	if err != nil {
		s.fail(w, err)
		return
	}
	ids := make([]string, len(alerts))
	for i, a := range alerts {
		a.Sample, a.SampleRaw = nil, "" // keep the list light; detail view loads them
		ids[i] = a.ID
	}
	if pending, err := alerting.PendingApprovals(r.Context(), s.Store.Pool, ids); err == nil {
		for _, a := range alerts {
			a.PendingApprovals = pending[a.ID]
		}
	}
	writeJSON(w, http.StatusOK, alerts)
}

func (s *Server) getAlert(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	ns, err := s.Store.Notifications(r.Context(), a.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if pending, err := alerting.PendingApprovals(r.Context(), s.Store.Pool, []string{a.ID}); err == nil {
		a.PendingApprovals = pending[a.ID]
	}
	writeJSON(w, http.StatusOK, map[string]any{"alert": a, "notifications": ns})
}

func (s *Server) transition(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			By string `json:"by"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
		by := strings.TrimSpace(body.By)
		if by == "" {
			by = "anonymous"
		}
		var a *alerting.Alert
		var err error
		if action == "ack" {
			a, err = s.Store.Ack(r.Context(), r.PathValue("id"), by)
		} else {
			a, err = s.Store.Resolve(r.Context(), r.PathValue("id"), by)
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		s.record(r, audit.Event{Actor: by, Action: "alert." + action, Subject: "alert:" + a.ID,
			Detail: map[string]string{"via": "ui", "title": a.Title}})
		s.publishStatus(r, a)
		writeJSON(w, http.StatusOK, a)
	}
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Stats(r.Context(), r.URL.Query().Get("team"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type ruleView struct {
	rules.Rule
	Fields  []string `json:"fields"`
	Enabled bool     `json:"enabled"`
}

func (s *Server) listRules(w http.ResponseWriter, _ *http.Request) {
	rs := s.Engine.Current()
	out := make([]ruleView, 0, len(rs.Rules))
	for _, c := range rs.Rules {
		out = append(out, ruleView{Rule: c.Rule, Fields: rules.ReferencedFields(c.Rule.Match), Enabled: c.Rule.IsEnabled()})
	}
	errs := rs.Errors
	if errs == nil {
		errs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out, "errors": errs, "loadedAt": rs.LoadedAt})
}

// spa serves the embedded UI and falls back to index.html for client routes.
func (s *Server) spa() http.Handler {
	if s.UI == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><title>Kardan</title><body style="font-family:sans-serif;padding:2rem">
<h1>Kardan API is running</h1><p>The web UI is not embedded in this build. Run <code>npm run build</code> in <code>web/</code>
and rebuild, or use <code>docker compose up --build</code>.</p><p><a href="/api/info">/api/info</a></p>`))
		})
	}
	files := http.FileServer(http.FS(s.UI))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.UI, p); err != nil {
			r.URL.Path = "/" // client-side route
		} else if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, alerting.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.Log.Error("api error", "err", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
