package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/undeluro/kardan-alerts/internal/alerting"
)

// PermanentError marks a delivery that will never succeed as sent (bad
// number, rejected address). It is dead-lettered at once instead of retried,
// and it does not count against the channel's circuit breaker.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return "permanent: " + e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// GatewayChannel delivers SMS and phone calls through an HTTP SMS/voice
// provider. In the demo it talks to cmd/fakegw; a real provider (Twilio,
// Vonage, MessageBird) is another implementation of the same small contract:
// POST the message, 2xx = accepted, 4xx = permanent, 5xx/timeouts = retry.
type GatewayChannel struct {
	Kind    string // sms | phone
	BaseURL string
	Token   string
	Client  *http.Client
}

type gatewayRequest struct {
	ID              string    `json:"id"` // notification id: idempotency key and reply reference
	To              string    `json:"to"`
	Text            string    `json:"text"`
	AlertID         string    `json:"alertId"`
	ShortCode       string    `json:"shortCode"`
	EventReceivedAt time.Time `json:"eventReceivedAt"`
	Kind            string    `json:"kind,omitempty"` // alert | digest | resolved | approval | escalation
}

func (c *GatewayChannel) Name() string { return c.Kind }

func (c *GatewayChannel) Send(ctx context.Context, n alerting.Notification) error {
	path := "/sms"
	if c.Kind == "phone" {
		path = "/call"
	}
	body, _ := json.Marshal(gatewayRequest{ID: n.ID, To: n.Address, Text: n.BodyText, AlertID: n.AlertID,
		ShortCode: alerting.ShortCode(n.AlertID), EventReceivedAt: n.EventReceivedAt, Kind: n.Kind})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return &PermanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", n.ID)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	cl := c.Client
	if cl == nil {
		cl = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("%s gateway unreachable: %w", c.Kind, err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("%s gateway %d: %s", c.Kind, resp.StatusCode, strings.TrimSpace(string(msg)))
	default:
		return &PermanentError{fmt.Errorf("%s gateway rejected the message (%d): %s", c.Kind, resp.StatusCode,
			strings.TrimSpace(string(msg)))}
	}
}
