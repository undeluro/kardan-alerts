package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tidwall/gjson"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
)

// Hub fans Redis pub/sub alert updates out to Server-Sent-Events clients
// connected to this replica.
type Hub struct {
	Redis *redis.Client
	Log   *slog.Logger

	mu      sync.RWMutex
	clients map[*sseClient]struct{}
}

type sseClient struct {
	team string
	ch   chan []byte
}

func (h *Hub) add(c *sseClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients == nil {
		h.clients = map[*sseClient]struct{}{}
	}
	h.clients[c] = struct{}{}
}

func (h *Hub) remove(c *sseClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

func (h *Hub) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		sub := h.Redis.Subscribe(ctx, alerting.UpdatesChannel)
		if _, err := sub.Receive(ctx); err != nil {
			_ = sub.Close()
			if ctx.Err() != nil {
				return nil
			}
			h.Log.Warn("live feed subscribe failed, retrying", "err", err)
			time.Sleep(time.Second)
			continue
		}
		ch := sub.Channel(redis.WithChannelSize(4096))
	loop:
		for {
			select {
			case <-ctx.Done():
				_ = sub.Close()
				return nil
			case msg, ok := <-ch:
				if !ok {
					break loop
				}
				h.broadcast([]byte(msg.Payload))
			}
		}
		_ = sub.Close()
	}
	return nil
}

func (h *Hub) broadcast(payload []byte) {
	team := gjson.GetBytes(payload, "team").String()
	h.mu.RLock()
	delivered := false
	for c := range h.clients {
		if c.team != "" && team != "" && c.team != team {
			continue // team "" = broadcast (e.g. new schema detected)
		}
		select {
		case c.ch <- payload:
			delivered = true
		default: // slow client: drop; it resyncs from the REST API on reconnect
		}
	}
	h.mu.RUnlock()

	if delivered && gjson.GetBytes(payload, "kind").String() == "created" {
		if ts := gjson.GetBytes(payload, "receivedAt"); ts.Exists() {
			if t, err := time.Parse(time.RFC3339Nano, ts.String()); err == nil {
				metrics.EndToEndLatency.WithLabelValues("ui").Observe(time.Since(t).Seconds())
			}
		}
	}
}

// ServeHTTP streams updates: GET /api/stream?team=rates
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	c := &sseClient{team: r.URL.Query().Get("team"), ch: make(chan []byte, 256)}
	h.add(c)
	metrics.SSEClients.Inc()
	defer func() {
		h.remove(c)
		metrics.SSEClients.Dec()
	}()

	fmt.Fprint(w, "retry: 2000\nevent: ready\ndata: {}\n\n")
	flusher.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-c.ch:
			fmt.Fprintf(w, "event: alert\ndata: %s\n\n", msg)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
