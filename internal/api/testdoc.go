package api

import (
	"encoding/json"
	"time"

	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

func testDoc(payload json.RawMessage) *schema.Doc {
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	return schema.NewDoc(&event.Envelope{
		ID:         "test",
		SourceID:   "test",
		ReceivedAt: time.Now().UTC(),
		Payload:    payload,
	})
}
