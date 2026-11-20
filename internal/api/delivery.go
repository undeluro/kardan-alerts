package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/directory"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
)

// ---- inbound replies from the SMS / voice gateway ------------------------------

// gatewayAuth protects inbound webhooks: a forged "ACK" must not silence an
// alert. Real providers sign requests (e.g. Twilio's X-Twilio-Signature); the
// demo gateway uses a shared bearer token.
func (s *Server) gatewayAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.GatewayToken != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.GatewayToken)) != 1 {
				writeErr(w, http.StatusUnauthorized, "invalid gateway token")
				return
			}
		}
		next(w, r)
	}
}

// inboundSMS handles replies such as "ACK", "ACK K7Q2", "RESOLVE K7Q2", "HELP".
//
//	POST /inbound/sms {"from": "+44 7700 900102", "text": "ACK K7Q2"} -> {"reply": "..."}
func (s *Server) inboundSMS(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	reply := func(outcome, text string) {
		metrics.InboundReplies.WithLabelValues("sms", outcome).Inc()
		writeJSON(w, http.StatusOK, map[string]string{"reply": text, "outcome": outcome})
	}
	person, ok := s.Directory().PersonByPhone(req.From)
	if !ok {
		reply("unknown_sender", "This number is not registered with Kardan.")
		return
	}
	words := strings.Fields(strings.ToUpper(req.Text))
	if len(words) == 0 {
		reply("empty", "Reply ACK <code> to acknowledge or RESOLVE <code> to resolve.")
		return
	}
	var action string
	switch words[0] {
	case "ACK", "ACKNOWLEDGE", "OK", "1", "Y", "YES":
		action = "ack"
	case "RESOLVE", "RESOLVED", "FIXED", "DONE", "CLOSE":
		action = "resolve"
	case "HELP", "?":
		reply("help", "Kardan: ACK <code> acknowledges, RESOLVE <code> resolves. Without a code, your latest alert is used.")
		return
	default:
		reply("unknown_command", fmt.Sprintf("Sorry, %q is not a command. Reply HELP for options.", words[0]))
		return
	}
	code := ""
	if len(words) > 1 {
		code = words[1]
	}
	a, err := s.Store.FindForReply(r.Context(), person.ID, code)
	if errors.Is(err, alerting.ErrNotFound) {
		if code != "" {
			reply("not_found", fmt.Sprintf("No open alert %s for you.", code))
		} else {
			reply("not_found", "You have no open alerts.")
		}
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	by := person.Name + " (SMS)"
	if action == "ack" {
		a, err = s.Store.Ack(r.Context(), a.ID, by)
	} else {
		a, err = s.Store.Resolve(r.Context(), a.ID, by)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.publishStatus(r, a)
	s.Log.Info("alert updated by SMS reply", "alert", a.ID, "action", action, "by", person.ID)
	verb := map[string]string{"ack": "Acknowledged", "resolve": "Resolved"}[action]
	reply(action, fmt.Sprintf("%s %s: %s", verb, alerting.ShortCode(a.ID), a.Title))
}

// inboundCall handles keypad presses on an alert call.
//
//	POST /inbound/call {"ref": "<notification id>", "digit": "1"} -> {"say": "..."}
func (s *Server) inboundCall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ref   string `json:"ref"`
		Digit string `json:"digit"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a, recipient, err := s.Store.NotificationAlert(r.Context(), req.Ref)
	if errors.Is(err, alerting.ErrNotFound) {
		metrics.InboundReplies.WithLabelValues("phone", "not_found").Inc()
		writeJSON(w, http.StatusOK, map[string]string{"say": "Sorry, this call is no longer valid."})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if req.Digit != "1" {
		metrics.InboundReplies.WithLabelValues("phone", "declined").Inc()
		writeJSON(w, http.StatusOK, map[string]string{"say": "No action taken. The alert stays open."})
		return
	}
	name := recipient
	if p, ok := s.Directory().Person(recipient); ok {
		name = p.Name
	}
	if a, err = s.Store.Ack(r.Context(), a.ID, name+" (phone)"); err != nil {
		s.fail(w, err)
		return
	}
	s.publishStatus(r, a)
	metrics.InboundReplies.WithLabelValues("phone", "ack").Inc()
	writeJSON(w, http.StatusOK, map[string]string{"say": "Acknowledged. Thank you."})
}

func (s *Server) publishStatus(r *http.Request, a *alerting.Alert) {
	a.Sample, a.SampleRaw = nil, ""
	if err := s.Publisher.Publish(r.Context(), []alerting.Update{{Kind: "status", Team: a.Team, Alert: a}}); err != nil {
		s.Log.Warn("publish status update", "err", err)
	}
}

// ---- deliveries -----------------------------------------------------------------

func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	ds, err := s.Store.Deliveries(r.Context(), q.Get("status"), q.Get("channel"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

func (s *Server) resend(w http.ResponseWriter, r *http.Request) {
	err := s.Store.Resend(r.Context(), r.PathValue("id"))
	if errors.Is(err, alerting.ErrNotFound) {
		writeErr(w, http.StatusConflict, "only dead, waiting or parked deliveries can be re-sent")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Info("delivery re-queued", "notification", r.PathValue("id"), "by", actor(r))
	w.WriteHeader(http.StatusNoContent)
}

type channelView struct {
	alerting.ChannelHealth
	Breaker  string `json:"breaker,omitempty"` // closed | half-open | open (worst across dispatcher replicas)
	Provider string `json:"provider"`
}

func (s *Server) channels(w http.ResponseWriter, r *http.Request) {
	health, err := s.Store.ChannelHealth(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	states := map[string]string{}
	if s.ChannelStates != nil {
		states = s.ChannelStates(r)
	}
	byName := map[string]*channelView{}
	for _, ch := range []string{"email", "sms", "phone"} {
		byName[ch] = &channelView{ChannelHealth: alerting.ChannelHealth{Channel: ch}}
	}
	for _, h := range health {
		if v, ok := byName[h.Channel]; ok {
			v.ChannelHealth = h
		}
	}
	provider := map[string]string{"email": "SMTP (Mailpit)", "sms": "SMS gateway (simulated)", "phone": "Voice gateway (simulated)"}
	out := make([]channelView, 0, len(byName))
	for name, v := range byName {
		v.Breaker = states[name]
		if v.Breaker == "" {
			v.Breaker = "closed"
		}
		v.Provider = provider[name]
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	writeJSON(w, http.StatusOK, out)
}

// ---- on-call and the demo clock -------------------------------------------------

func (s *Server) onCall(w http.ResponseWriter, _ *http.Request) {
	dir := s.Directory()
	now := s.Clock.Now()
	out := []directory.OnCallNow{}
	for _, t := range dir.Teams {
		if oc, ok := dir.WhoIsOnCall(t.ID, now); ok {
			out = append(out, oc)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "onCall": out})
}

func (s *Server) clockView() map[string]any {
	return map[string]any{
		"now":           s.Clock.Now(),
		"offsetMinutes": int(s.Clock.Offset() / time.Minute),
		"demoMode":      s.Clock.Enabled,
	}
}

func (s *Server) getClock(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.clockView())
}

// setClock shifts business time for every replica (demo mode only):
// {"offsetMinutes": 600} or {"at": "2026-11-20T21:00:00Z"}; {} resets.
func (s *Server) setClock(w http.ResponseWriter, r *http.Request) {
	if !s.Clock.Enabled {
		writeErr(w, http.StatusForbidden, "the business clock can only be shifted in demo mode (DEMO_MODE=true)")
		return
	}
	var req struct {
		OffsetMinutes *int       `json:"offsetMinutes"`
		At            *time.Time `json:"at"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var d time.Duration
	switch {
	case req.At != nil:
		d = time.Until(*req.At).Round(time.Minute)
	case req.OffsetMinutes != nil:
		d = time.Duration(*req.OffsetMinutes) * time.Minute
	}
	if d > 30*24*time.Hour || d < -30*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "offset must be within 30 days")
		return
	}
	if err := s.Clock.Set(r.Context(), d); err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Info("business clock shifted", "offset", d, "by", actor(r))
	writeJSON(w, http.StatusOK, s.clockView())
}
