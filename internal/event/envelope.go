// Package event defines the normalized envelope every source produces.
package event

import (
	"crypto/rand"
	"encoding/json"
	"time"

	"github.com/oklog/ulid/v2"
)

// Envelope wraps an incoming message without assuming anything about its
// schema. The original payload is kept verbatim; rules address its fields by
// path, so a schema the service has never seen works without code changes.
type Envelope struct {
	ID         string            `json:"id"`
	SourceID   string            `json:"sourceId"`
	ReceivedAt time.Time         `json:"receivedAt"`
	Payload    json.RawMessage   `json:"payload"`
	Meta       map[string]string `json:"meta,omitempty"` // transport details: topic, file, line, content type
}

// NewID returns a lexicographically sortable unique id (ULID).
func NewID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
