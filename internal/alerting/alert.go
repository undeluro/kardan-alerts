// Package alerting owns alert state: the write path used by processors, the
// read/ack/resolve path used by the API, and live update fan-out.
package alerting

import (
	"encoding/json"
	"time"

	"github.com/undeluro/kardan-alerts/internal/rules"
)

type Status string

const (
	StatusOpen     Status = "open"
	StatusAcked    Status = "acked"
	StatusResolved Status = "resolved"
)

type Alert struct {
	ID           string                `json:"id"`
	Fingerprint  string                `json:"fingerprint"`
	RuleID       string                `json:"ruleId"`
	Team         string                `json:"team"`
	Status       Status                `json:"status"`
	Severity     string                `json:"severity"`
	Color        string                `json:"color"`
	Title        string                `json:"title"`
	Body         string                `json:"body"`
	Fields       []rules.RenderedField `json:"fields"`
	SourceID     string                `json:"sourceId"`
	FirstEventID string                `json:"firstEventId"`
	LastEventID  string                `json:"lastEventId"`
	Sample       json.RawMessage       `json:"sample,omitempty"`
	Count        int64                 `json:"count"`
	FirstSeen    time.Time             `json:"firstSeen"`
	LastSeen     time.Time             `json:"lastSeen"`
	AckedBy      *string               `json:"ackedBy,omitempty"`
	AckedAt      *time.Time            `json:"ackedAt,omitempty"`
	ResolvedBy   *string               `json:"resolvedBy,omitempty"`
	ResolvedAt   *time.Time            `json:"resolvedAt,omitempty"`
	CreatedAt    time.Time             `json:"createdAt"`
	UpdatedAt    time.Time             `json:"updatedAt"`
}

// Update is pushed to live-feed clients.
type Update struct {
	Kind  string `json:"kind"` // created | count | status
	Team  string `json:"team"`
	Alert *Alert `json:"alert,omitempty"` // created, status

	// count updates carry only what changed, to stay small under load
	ID       string    `json:"id,omitempty"`
	Count    int64     `json:"count,omitempty"`
	LastSeen time.Time `json:"lastSeen,omitempty"`

	// ReceivedAt is when the triggering event was ingested (latency metrics).
	ReceivedAt time.Time `json:"receivedAt,omitempty"`
}

// Notification is one outbox row: a message for one person on one channel.
type Notification struct {
	ID              string     `json:"id"`
	AlertID         string     `json:"alertId"`
	Recipient       string     `json:"recipient"`
	Channel         string     `json:"channel"`
	Address         string     `json:"address"`
	Subject         string     `json:"subject"`
	BodyText        string     `json:"bodyText"`
	BodyHTML        string     `json:"-"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	NextAttemptAt   time.Time  `json:"nextAttemptAt"`
	LastError       string     `json:"lastError,omitempty"`
	EventReceivedAt time.Time  `json:"eventReceivedAt"`
	CreatedAt       time.Time  `json:"createdAt"`
	SentAt          *time.Time `json:"sentAt,omitempty"`
}
