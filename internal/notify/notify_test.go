package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/rules"
)

func sampleAlert() *alerting.Alert {
	return &alerting.Alert{
		ID: "01ABC", Team: "settlements", Severity: "critical", Color: "red", SourceID: "workflow-engine",
		Title:  "Settlement WF-1 failed at validate-counterparty",
		Body:   "TIMEOUT: Counterparty service did not respond in 5000 ms",
		Fields: []rules.RenderedField{{Label: "Step", Value: "validate-counterparty"}, {Label: "Note", Value: "<b>x</b>"}},
	}
}

func TestRenderPerChannel(t *testing.T) {
	r := Renderer{PublicURL: "http://kardan.local"}
	a := sampleAlert()

	sms := r.Render("sms", a)
	if !strings.HasPrefix(sms.BodyText, "[CRITICAL] Settlement") || !strings.HasSuffix(sms.BodyText, "http://kardan.local/alerts/01ABC") {
		t.Errorf("sms = %q", sms.BodyText)
	}

	phone := r.Render("phone", a)
	want := "Kardan alert. Severity critical. Settlement WF-1 failed at validate-counterparty. TIMEOUT: Counterparty service did not respond in 5000 ms. Press 1 to acknowledge."
	if phone.BodyText != want {
		t.Errorf("phone =\n%q\nwant\n%q", phone.BodyText, want)
	}

	email := r.Render("email", a)
	if email.Subject != "[CRITICAL] Settlement WF-1 failed at validate-counterparty" {
		t.Errorf("subject = %q", email.Subject)
	}
	if !strings.Contains(email.BodyText, "Step: validate-counterparty") {
		t.Errorf("text body missing fields: %q", email.BodyText)
	}
	if strings.Contains(email.BodyHTML, "<b>x</b>") || !strings.Contains(email.BodyHTML, "&lt;b&gt;x&lt;/b&gt;") {
		t.Error("HTML body must escape event values")
	}
}

func TestLongSMSIsTruncated(t *testing.T) {
	a := sampleAlert()
	a.Title = strings.Repeat("very long title ", 30)
	sms := Renderer{PublicURL: "http://k"}.Render("sms", a)
	if n := len([]rune(strings.TrimSuffix(sms.BodyText, " http://k/alerts/01ABC"))); n > 140 {
		t.Errorf("sms text is %d runes, want <= 140", n)
	}
}

func TestBackoff(t *testing.T) {
	for attempt := 1; attempt <= 20; attempt++ {
		d := Backoff(attempt)
		base := time.Second << min(attempt-1, 9)
		if base > 5*time.Minute {
			base = 5 * time.Minute
		}
		if d < base/2 || d > base {
			t.Errorf("attempt %d: %v outside [%v, %v]", attempt, d, base/2, base)
		}
	}
}
