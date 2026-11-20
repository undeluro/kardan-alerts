// Package ingest accepts events from sources and appends them to the events
// topic. It never interprets the payload: schema handling happens downstream.
package ingest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
)

const maxBody = 4 << 20 // 4 MiB per request

var sourceIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// Producer appends envelopes to the events topic.
type Producer struct {
	Kafka *kgo.Client
	Topic string
}

// Append returns only after the broker has acknowledged every record
// (acks=all, idempotent producer), so an accepted event is never lost.
func (p *Producer) Append(ctx context.Context, envs []*event.Envelope) error {
	recs := make([]*kgo.Record, len(envs))
	for i, e := range envs {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		recs[i] = &kgo.Record{Topic: p.Topic, Value: b}
	}
	start := time.Now()
	err := p.Kafka.ProduceSync(ctx, recs...).FirstErr()
	metrics.IngestDuration.Observe(time.Since(start).Seconds())
	return err
}

// HTTPHandler implements the webhook source:
//
//	POST /ingest/{source}         one event (JSON, or any text treated as a log line)
//	POST /ingest/{source}/batch   JSON array or newline-delimited JSON/text
type HTTPHandler struct {
	Producer *Producer
	Log      *slog.Logger
}

func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /ingest/{source}", h.handle(false))
	mux.HandleFunc("POST /ingest/{source}/batch", h.handle(true))
}

func (h *HTTPHandler) handle(batch bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		source := r.PathValue("source")
		if !sourceIDPattern.MatchString(source) {
			metrics.IngestRejected.WithLabelValues("bad_source").Inc()
			writeErr(w, http.StatusBadRequest, "source id must match [A-Za-z0-9._-]{1,64}")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			metrics.IngestRejected.WithLabelValues("too_large").Inc()
			writeErr(w, http.StatusRequestEntityTooLarge, "body exceeds 4 MiB")
			return
		}
		body = bytes.TrimSpace(body)
		if len(body) == 0 {
			metrics.IngestRejected.WithLabelValues("empty").Inc()
			writeErr(w, http.StatusBadRequest, "empty body")
			return
		}

		var payloads []json.RawMessage
		if batch {
			payloads = splitBatch(body)
		} else {
			payloads = []json.RawMessage{toPayload(body)}
		}

		now := time.Now().UTC()
		meta := map[string]string{"transport": "http"}
		if ct := r.Header.Get("Content-Type"); ct != "" {
			meta["contentType"] = ct
		}
		envs := make([]*event.Envelope, len(payloads))
		ids := make([]string, len(payloads))
		for i, p := range payloads {
			envs[i] = &event.Envelope{ID: event.NewID(), SourceID: source, ReceivedAt: now, Payload: p, Meta: meta}
			ids[i] = envs[i].ID
		}
		if err := h.Producer.Append(r.Context(), envs); err != nil {
			metrics.IngestRejected.WithLabelValues("broker_unavailable").Inc()
			h.Log.Error("append to events topic", "source", source, "events", len(envs), "err", err)
			writeErr(w, http.StatusServiceUnavailable, "event log unavailable, retry later")
			return
		}
		metrics.EventsIngested.WithLabelValues(source).Add(float64(len(envs)))

		resp := map[string]any{"accepted": len(envs)}
		if len(ids) <= 100 {
			resp["ids"] = ids
		}
		writeJSON(w, http.StatusAccepted, resp)
	}
}

// toPayload keeps valid JSON as-is and wraps anything else (a log line, a FIX
// message, XML) as {"message": "..."} so rules can still match on it.
// Format-specific parsers (FIX, ISO 20022, grok) plug in here later.
func toPayload(b []byte) json.RawMessage {
	if json.Valid(b) && (b[0] == '{' || b[0] == '[') {
		return json.RawMessage(b)
	}
	wrapped, _ := json.Marshal(map[string]string{"message": string(b)})
	return wrapped
}

func splitBatch(body []byte) []json.RawMessage {
	if body[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(body, &arr); err == nil {
			return arr
		}
	}
	var out []json.RawMessage
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), maxBody)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		cp := append([]byte(nil), line...)
		out = append(out, toPayload(cp))
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(msg)})
}
