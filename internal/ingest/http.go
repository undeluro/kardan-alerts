// Package ingest accepts events from sources (HTTP, Kafka, files), parses
// non-JSON formats into JSON, and appends them to the events topic. It never
// applies rules: schema handling and matching happen downstream.
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
	"github.com/undeluro/kardan-alerts/internal/ingest/parse"
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
	Parsers  func(source string) parse.Parser // per-source parser from sources.yaml; auto-detect otherwise
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

		var messages [][]byte
		if batch {
			messages = splitBatch(body)
		} else {
			messages = [][]byte{body}
		}

		meta := map[string]string{"transport": "http"}
		if ct := r.Header.Get("Content-Type"); ct != "" {
			meta["contentType"] = ct
		}
		parser := parse.Parser(parse.Auto{})
		if h.Parsers != nil {
			parser = h.Parsers(source)
		}
		envs := make([]*event.Envelope, len(messages))
		ids := make([]string, len(messages))
		for i, m := range messages {
			envs[i] = NewEnvelope(parser, source, m, meta)
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

// splitBatch accepts a JSON array or one message per line (NDJSON, log lines,
// FIX messages...).
func splitBatch(body []byte) [][]byte {
	if body[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(body, &arr); err == nil {
			out := make([][]byte, len(arr))
			for i, m := range arr {
				out[i] = m
			}
			return out
		}
	}
	var out [][]byte
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), maxBody)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		out = append(out, append([]byte(nil), line...))
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
