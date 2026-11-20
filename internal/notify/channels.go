package notify

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/wneessen/go-mail"

	"github.com/undeluro/kardan-alerts/internal/alerting"
)

// Channel delivers one notification. Adding a real provider (Twilio, SES,
// PagerDuty...) means implementing this interface and registering it; nothing
// else in the pipeline changes.
type Channel interface {
	Name() string
	Send(ctx context.Context, n alerting.Notification) error
}

// SMTPChannel sends email. In docker-compose it points at Mailpit.
type SMTPChannel struct {
	Host string
	Port int
	From string
}

func NewSMTPChannel(addr, from string) (*SMTPChannel, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("SMTP_ADDR %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("SMTP_ADDR %q: bad port", addr)
	}
	return &SMTPChannel{Host: host, Port: port, From: from}, nil
}

func (c *SMTPChannel) Name() string { return "email" }

func (c *SMTPChannel) Send(ctx context.Context, n alerting.Notification) error {
	m := mail.NewMsg()
	if err := m.From(c.From); err != nil {
		return err
	}
	if err := m.To(n.Address); err != nil {
		return fmt.Errorf("bad address %q: %w", n.Address, err)
	}
	m.Subject(n.Subject)
	m.SetBodyString(mail.TypeTextPlain, n.BodyText)
	if n.BodyHTML != "" {
		m.AddAlternativeString(mail.TypeTextHTML, n.BodyHTML)
	}
	cl, err := mail.NewClient(c.Host, mail.WithPort(c.Port), mail.WithTLSPolicy(mail.NoTLS),
		mail.WithTimeout(10*time.Second))
	if err != nil {
		return err
	}
	return cl.DialAndSendWithContext(ctx, m)
}
