// Package fakegw is a stand-in SMS and voice provider for demos and load
// tests. It behaves like a real provider from Kardan's point of view
// (HTTP API, idempotency keys, inbound webhooks for replies), lets a
// presenter inject failures, shows deliveries on a phone simulator page, and
// measures ingest-to-handset latency independently of Kardan's own metrics.
package fakegw

import (
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed simulator.html
var simulatorHTML []byte

type Message struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`      // sms | call | reply | system
	Direction string    `json:"direction"` // out: to the handset, in: from the handset
	Phone     string    `json:"phone"`
	Text      string    `json:"text"`
	Ref       string    `json:"ref,omitempty"` // Kardan notification id
	AlertID   string    `json:"alertId,omitempty"`
	ShortCode string    `json:"shortCode,omitempty"`
	Status    string    `json:"status,omitempty"` // calls: ringing | acknowledged | declined
	At        time.Time `json:"at"`
	LatencyMs float64   `json:"latencyMs,omitempty"`
}

// Faults simulate a misbehaving provider.
type Faults struct {
	Mode      string  `json:"mode"`      // ok | flaky | down
	ErrorRate float64 `json:"errorRate"` // flaky: share of requests answered 503
	LatencyMs int     `json:"latencyMs"` // added to every accepted request
}

type logEntry struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	To      string    `json:"to"`
	Outcome string    `json:"outcome"` // accepted | duplicate | rejected (503) | ...
}

type Server struct {
	KardanURL string // for inbound webhooks and the people list
	Token     string
	Log       *slog.Logger

	mu       sync.Mutex
	msgs     []Message
	seen     map[string]bool
	faults   Faults
	subs     map[chan []byte]struct{}
	lat      []float64
	received int64
	rejected int64
	since    time.Time
	requests []logEntry
	seq      int64
}

const (
	maxMessages = 3000
	maxLatency  = 500_000
)

func New(kardanURL, token string, log *slog.Logger) *Server {
	return &Server{KardanURL: strings.TrimRight(kardanURL, "/"), Token: token, Log: log,
		seen: map[string]bool{}, subs: map[chan []byte]struct{}{}, faults: Faults{Mode: "ok", ErrorRate: 0.3},
		since: time.Now()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sms", s.auth(s.accept("sms")))
	mux.HandleFunc("POST /call", s.auth(s.accept("call")))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(simulatorHTML)
	})
	mux.HandleFunc("GET /api/messages", s.listMessages)
	mux.HandleFunc("GET /api/stream", s.stream)
	mux.HandleFunc("GET /api/faults", s.getFaults)
	mux.HandleFunc("PUT /api/faults", s.setFaults)
	mux.HandleFunc("POST /api/reply", s.reply)
	mux.HandleFunc("POST /api/press", s.press)
	mux.HandleFunc("GET /api/people", s.people)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("POST /api/stats/reset", s.resetStats)
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

type sendRequest struct {
	ID              string    `json:"id"`
	To              string    `json:"to"`
	Text            string    `json:"text"`
	AlertID         string    `json:"alertId"`
	ShortCode       string    `json:"shortCode"`
	EventReceivedAt time.Time `json:"eventReceivedAt"`
}

// accept is the provider API: POST /sms or /call.
func (s *Server) accept(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req sendRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.To) == "" {
			http.Error(w, "missing destination number", http.StatusUnprocessableEntity)
			return
		}
		s.mu.Lock()
		f := s.faults
		s.mu.Unlock()
		if f.LatencyMs > 0 {
			time.Sleep(time.Duration(f.LatencyMs) * time.Millisecond)
		}
		if f.Mode == "down" || (f.Mode == "flaky" && rand.Float64() < f.ErrorRate) {
			s.record(logEntry{At: time.Now(), Kind: kind, To: req.To, Outcome: "rejected (503)"})
			s.mu.Lock()
			s.rejected++
			s.mu.Unlock()
			http.Error(w, "provider temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			key = req.ID
		}
		s.mu.Lock()
		dup := key != "" && s.seen[key]
		if key != "" {
			s.seen[key] = true
		}
		s.mu.Unlock()
		if dup {
			s.record(logEntry{At: time.Now(), Kind: kind, To: req.To, Outcome: "duplicate (ignored)"})
			writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
			return
		}
		m := Message{Kind: kind, Direction: "out", Phone: req.To, Text: req.Text, Ref: req.ID, AlertID: req.AlertID,
			ShortCode: req.ShortCode, At: time.Now()}
		if kind == "call" {
			m.Status = "ringing"
		}
		if !req.EventReceivedAt.IsZero() {
			m.LatencyMs = float64(time.Since(req.EventReceivedAt).Microseconds()) / 1000
		}
		m = s.add(m)
		s.record(logEntry{At: time.Now(), Kind: kind, To: req.To, Outcome: "accepted"})
		writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "id": m.ID})
	}
}

func (s *Server) add(m Message) Message {
	s.mu.Lock()
	s.seq++
	m.ID = fmt.Sprintf("m%06d", s.seq)
	s.msgs = append(s.msgs, m)
	if len(s.msgs) > maxMessages {
		s.msgs = s.msgs[len(s.msgs)-maxMessages:]
	}
	if m.Direction == "out" && m.Kind != "reply" && m.Kind != "system" {
		s.received++
		if m.LatencyMs > 0 {
			s.lat = append(s.lat, m.LatencyMs)
			if len(s.lat) > maxLatency {
				s.lat = s.lat[len(s.lat)-maxLatency:]
			}
		}
	}
	s.mu.Unlock()
	s.broadcast("message", m)
	return m
}

func (s *Server) update(id string, fn func(*Message)) (Message, bool) {
	s.mu.Lock()
	var out Message
	found := false
	for i := len(s.msgs) - 1; i >= 0; i-- {
		if s.msgs[i].ID == id {
			fn(&s.msgs[i])
			out, found = s.msgs[i], true
			break
		}
	}
	s.mu.Unlock()
	if found {
		s.broadcast("update", out)
	}
	return out, found
}

func (s *Server) record(e logEntry) {
	s.mu.Lock()
	s.requests = append(s.requests, e)
	if len(s.requests) > 200 {
		s.requests = s.requests[len(s.requests)-200:]
	}
	s.mu.Unlock()
	s.broadcast("request", e)
}

func (s *Server) broadcast(event string, v any) {
	b, _ := json.Marshal(v)
	frame := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, b))
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- frame:
		default:
		}
	}
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := make(chan []byte, 512)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()
	fmt.Fprint(w, "retry: 2000\nevent: ready\ndata: {}\n\n")
	fl.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case f := <-ch:
			_, _ = w.Write(f)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) listMessages(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	msgs := append([]Message(nil), s.msgs...)
	reqs := append([]logEntry(nil), s.requests...)
	s.mu.Unlock()
	if len(msgs) > 500 {
		msgs = msgs[len(msgs)-500:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs, "requests": reqs})
}

func (s *Server) getFaults(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	f := s.faults
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) setFaults(w http.ResponseWriter, r *http.Request) {
	var f Faults
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&f); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch f.Mode {
	case "ok", "flaky", "down":
	default:
		http.Error(w, "mode must be ok, flaky or down", http.StatusBadRequest)
		return
	}
	f.ErrorRate = min(max(f.ErrorRate, 0), 1)
	f.LatencyMs = min(max(f.LatencyMs, 0), 10_000)
	s.mu.Lock()
	s.faults = f
	s.mu.Unlock()
	s.Log.Info("faults changed", "mode", f.Mode, "errorRate", f.ErrorRate, "latencyMs", f.LatencyMs)
	s.broadcast("faults", f)
	writeJSON(w, http.StatusOK, f)
}

// reply simulates the person texting back; it is forwarded to Kardan's
// inbound webhook like a provider would.
func (s *Server) reply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
		Text  string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		http.Error(w, "phone and text are required", http.StatusBadRequest)
		return
	}
	s.add(Message{Kind: "sms", Direction: "in", Phone: req.Phone, Text: req.Text, At: time.Now()})
	var resp struct {
		Reply string `json:"reply"`
	}
	if err := s.callKardan(r.Context(), "/inbound/sms", map[string]string{"from": req.Phone, "text": req.Text}, &resp); err != nil {
		resp.Reply = "(Kardan unreachable: " + err.Error() + ")"
	}
	m := s.add(Message{Kind: "reply", Direction: "out", Phone: req.Phone, Text: resp.Reply, At: time.Now()})
	writeJSON(w, http.StatusOK, m)
}

// press simulates a keypad press during an alert call.
func (s *Server) press(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string `json:"id"`
		Digit string `json:"digit"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var call Message
	s.mu.Lock()
	for i := len(s.msgs) - 1; i >= 0; i-- {
		if s.msgs[i].ID == req.ID {
			call = s.msgs[i]
			break
		}
	}
	s.mu.Unlock()
	if call.Kind != "call" {
		http.Error(w, "no such call", http.StatusNotFound)
		return
	}
	var resp struct {
		Say string `json:"say"`
	}
	if err := s.callKardan(r.Context(), "/inbound/call", map[string]string{"ref": call.Ref, "digit": req.Digit, "from": call.Phone}, &resp); err != nil {
		resp.Say = "(Kardan unreachable: " + err.Error() + ")"
	}
	status := "declined"
	if req.Digit == "1" {
		status = "acknowledged"
	}
	updated, _ := s.update(call.ID, func(m *Message) { m.Status = status })
	s.add(Message{Kind: "system", Direction: "out", Phone: call.Phone, Text: "Pressed " + req.Digit + " — " + resp.Say, At: time.Now()})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) callKardan(ctx context.Context, path string, body any, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.KardanURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("%d %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// people proxies Kardan's directory so the simulator can show names.
func (s *Server) people(w http.ResponseWriter, r *http.Request) {
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, s.KardanURL+"/api/teams", nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"people": []any{}})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.Copy(w, resp.Body)
}

// stats reports latency from Kardan accepting the event to this gateway
// receiving the message: an end-to-end figure measured outside Kardan.
func (s *Server) stats(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	lat := append([]float64(nil), s.lat...)
	out := map[string]any{"received": s.received, "rejected": s.rejected, "since": s.since,
		"samples": len(lat)}
	s.mu.Unlock()
	if len(lat) > 0 {
		sort.Float64s(lat)
		q := func(p float64) float64 { return lat[min(len(lat)-1, int(p*float64(len(lat))))] }
		out["p50Ms"], out["p95Ms"], out["p99Ms"], out["maxMs"] = q(0.50), q(0.95), q(0.99), lat[len(lat)-1]
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) resetStats(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.lat, s.received, s.rejected, s.since = nil, 0, 0, time.Now()
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
