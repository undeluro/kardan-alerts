package processor

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/directory"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/notify"
	"github.com/undeluro/kardan-alerts/internal/privacy"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

const teams = `
people:
  - id: anna
    name: Anna
    email: anna@example.com
    phone: "+44 7700 900101"
    timezone: Europe/London
    preferences:
      - { severity: critical, channels: [sms], between: "08:00-20:00" }
      - { severity: critical, channels: [email] }
  - { id: sam,   name: Sam,   email: sam@example.com,   phone: "+44 7700 900102", timezone: Europe/London }
  - { id: maria, name: Maria, email: maria@example.com, phone: "+44 7700 900103", timezone: Europe/London }
teams:
  - id: payments
    members: [anna]
  - id: settlements
    members: [sam, maria]
    onCall:
      timezone: Europe/London
      rotation: { every: week, start: "2026-11-02T09:00", people: [sam, maria] }
`

func setup(t *testing.T) (*Processor, *directory.Directory, *group) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "teams.yaml")
	if err := os.WriteFile(path, []byte(teams), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, err := directory.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := rules.Compile(rules.Rule{
		ID: "payments-rejected", Team: "payments",
		Match: rules.Condition{Field: "status", Op: "eq", Value: "RJCT"},
		Routing: []rules.Route{
			{To: "oncall:settlements", Channels: []string{"phone", "sms"}},
			{To: "user:anna", Channels: []string{"preferences", "ui"}},
		},
		Presentation: rules.Presentation{
			Title:    "Payment from {{debtor}} rejected ({{reasonCode}})",
			Fields:   []string{"IBAN=debtorIban", "Amount=amount"},
			Severity: "critical",
		},
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"status":"RJCT","reasonCode":"AC04","debtor":"Acme Industrial GmbH","debtorIban":"DE89370400440532013000","amount":250000}`
	doc := schema.NewDoc(&event.Envelope{ID: "e1", Payload: json.RawMessage(payload), ReceivedAt: time.Now()})
	p := &Processor{
		Privacy:   privacy.Default,
		Fallbacks: map[string]string{"phone": "sms", "sms": "email"},
		Renderer:  notify.Renderer{PublicURL: "http://k"},
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	kinds := map[string]string{"debtorIban": schema.KindIBAN, "debtor": schema.KindPartyName}
	classify := p.classifier(doc, kinds)
	g := &group{rule: rule, first: doc, kinds: kinds, recvAt: time.Now(),
		rendered: rule.RenderWith(doc.Get, func(path string) bool { return classify(path) > privacy.Public })}
	return p, dir, g
}

func alertFor(g *group) *alerting.Alert {
	return &alerting.Alert{ID: "01JALERTK7Q2", Team: "payments", Severity: g.rendered.Severity, Title: g.rendered.Title,
		Fields: g.rendered.Fields, Color: g.rendered.Color}
}

func index(ns []alerting.Notification) map[string]alerting.Notification {
	out := map[string]alerting.Notification{}
	for _, n := range ns {
		out[n.Recipient+"|"+n.Channel] = n
	}
	return out
}

func TestRoutingMaskingAndFallbacks(t *testing.T) {
	p, dir, g := setup(t)

	// The UI alert keeps everything, with sensitive fields flagged.
	if !strings.Contains(g.rendered.Title, "Acme Industrial GmbH") {
		t.Errorf("UI title should be unmasked: %q", g.rendered.Title)
	}
	if !g.rendered.Fields[0].Sensitive || g.rendered.Fields[1].Sensitive {
		t.Errorf("sensitive flags: %+v", g.rendered.Fields)
	}

	// Week 2 of the rotation -> Maria is on call; 09:00 London -> Anna gets SMS.
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	ns := index(p.notificationsFor(alertFor(g), g, dir, now))

	for _, key := range []string{"maria|phone", "maria|sms", "anna|sms"} {
		if n, ok := ns[key]; !ok || n.Status != "" {
			t.Errorf("%s should be a primary delivery, got %+v", key, n)
		}
	}
	if _, ok := ns["sam|phone"]; ok {
		t.Error("sam is not on call in week 2")
	}
	if ns["maria|phone"].Reason == "" || !strings.Contains(ns["maria|phone"].Reason, "rotation") {
		t.Errorf("reason should explain the on-call choice: %q", ns["maria|phone"].Reason)
	}

	// SMS: public channel -> IBAN and the debtor's name are masked.
	sms := ns["anna|sms"].BodyText
	if strings.Contains(sms, "Acme") || !strings.Contains(sms, "A. I. G.") || !strings.Contains(sms, "Reply ACK K7Q2") {
		t.Errorf("sms body = %q", sms)
	}

	// Fallbacks: sms -> email parked as standby; phone -> sms skipped because
	// Maria already gets an SMS.
	fb, ok := ns["anna|email"]
	if !ok || fb.Status != "standby" || fb.FallbackFor != ns["anna|sms"].ID {
		t.Errorf("anna email fallback = %+v", fb)
	}
	if strings.Contains(fb.BodyText, "DE89370400440532013000") || !strings.Contains(fb.BodyText, "****3000") {
		t.Errorf("email (internal) must mask the IBAN: %q", fb.BodyText)
	}
	count := 0
	for _, n := range ns {
		if n.Recipient == "maria" && n.Channel == "sms" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("maria should get exactly one SMS, got %d", count)
	}

	// 21:00 London -> Anna's preference switches to email (primary, not standby).
	late := index(p.notificationsFor(alertFor(g), g, dir, time.Date(2026, 11, 10, 21, 0, 0, 0, time.UTC)))
	if n, ok := late["anna|email"]; !ok || n.Status != "" {
		t.Errorf("after 20:00 Anna should get email as primary: %+v", n)
	}
	if _, ok := late["anna|sms"]; ok {
		t.Error("after 20:00 Anna should not get SMS")
	}
}
