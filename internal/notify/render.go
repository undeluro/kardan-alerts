// Package notify turns alerts into channel messages and delivers them from the
// outbox with retries.
package notify

import (
	"bytes"
	"fmt"
	"html/template"
	"regexp"
	"strings"
	"time"

	"github.com/undeluro/kardan-alerts/internal/alerting"
)

// Message is the channel-specific rendering of an alert for one recipient.
type Message struct {
	Subject  string
	BodyText string
	BodyHTML string
}

// Renderer formats alerts per channel: full detail where it fits (email, UI),
// a title and short link where it does not (SMS), speech for phone calls.
type Renderer struct {
	PublicURL string
}

func (r Renderer) link(a *alerting.Alert) string {
	return fmt.Sprintf("%s/alerts/%s", r.PublicURL, a.ID)
}

func (r Renderer) Render(channel string, a *alerting.Alert) Message {
	sev := strings.ToUpper(a.Severity)
	switch channel {
	case "sms":
		text := fmt.Sprintf("[%s] %s", sev, a.Title)
		if len([]rune(text)) > 120 {
			text = string([]rune(text)[:119]) + "…"
		}
		code := alerting.ShortCode(a.ID)
		return Message{Subject: a.Title, BodyText: fmt.Sprintf("%s · Reply ACK %s · %s", text, code, r.link(a))}
	case "phone":
		parts := []string{"Kardan alert", "Severity " + a.Severity, a.Title}
		if a.Body != "" {
			parts = append(parts, a.Body)
		}
		parts = append(parts, "Press 1 to acknowledge")
		for i, p := range parts {
			parts[i] = strings.TrimRight(strings.TrimSpace(p), ".")
		}
		return Message{Subject: a.Title, BodyText: strings.Join(parts, ". ") + "."}
	default: // email
		var t strings.Builder
		fmt.Fprintf(&t, "%s\n\nSeverity: %s\nTeam: %s\nSource: %s\n", a.Title, a.Severity, a.Team, a.SourceID)
		if a.Body != "" {
			fmt.Fprintf(&t, "\n%s\n", a.Body)
		}
		if len(a.Fields) > 0 {
			t.WriteString("\n")
			for _, f := range a.Fields {
				fmt.Fprintf(&t, "%s: %s\n", f.Label, f.Value)
			}
		}
		fmt.Fprintf(&t, "\nOpen in Kardan: %s\nOr reply ACK %s to the SMS to acknowledge.\n", r.link(a), alerting.ShortCode(a.ID))

		var h bytes.Buffer
		_ = emailTemplate.Execute(&h, map[string]any{"A": a, "Link": r.link(a), "Hex": colorHex(a.Color)})
		return Message{Subject: fmt.Sprintf("[%s] %s", sev, a.Title), BodyText: t.String(), BodyHTML: h.String()}
	}
}

func colorHex(c string) string {
	switch c {
	case "red":
		return "#dc2626"
	case "orange":
		return "#ea580c"
	case "amber":
		return "#d97706"
	case "blue":
		return "#2563eb"
	case "green":
		return "#16a34a"
	default:
		return "#4b5563"
	}
}

var emailTemplate = template.Must(template.New("email").Parse(`<!doctype html>
<html><body style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;background:#f6f7f9;margin:0;padding:24px">
<table role="presentation" width="100%" style="max-width:640px;margin:auto;background:#fff;border-radius:8px;overflow:hidden;border:1px solid #e5e7eb">
<tr><td style="background:{{.Hex}};color:#fff;padding:12px 20px;font-size:13px;letter-spacing:.04em;text-transform:uppercase">
{{.A.Severity}} · {{.A.Team}} · {{.A.SourceID}}</td></tr>
<tr><td style="padding:20px">
<h1 style="font-size:20px;margin:0 0 12px;color:#111827">{{.A.Title}}</h1>
{{if .A.Body}}<p style="color:#374151;white-space:pre-wrap">{{.A.Body}}</p>{{end}}
{{if .A.Fields}}<table style="border-collapse:collapse;margin:12px 0;font-size:14px">
{{range .A.Fields}}<tr><td style="padding:4px 16px 4px 0;color:#6b7280">{{.Label}}</td><td style="padding:4px 0;color:#111827;font-family:ui-monospace,monospace">{{.Value}}</td></tr>{{end}}
</table>{{end}}
<p style="margin-top:20px"><a href="{{.Link}}" style="background:#111827;color:#fff;padding:10px 16px;border-radius:6px;text-decoration:none">Open alert</a></p>
</td></tr></table></body></html>`))

var sevPrefix = regexp.MustCompile(`^\[[A-Z]+\]\s*`)

// RenderAllClear tells someone who was paged that the alert resolved itself.
// subject is what they were originally sent (already masked for the channel).
func (r Renderer) RenderAllClear(channel, alertID, subject, reason string) Message {
	title := sevPrefix.ReplaceAllString(subject, "")
	link := fmt.Sprintf("%s/alerts/%s", r.PublicURL, alertID)
	switch channel {
	case "email":
		text := fmt.Sprintf("Resolved: %s\n\n%s\n\nNo action needed. Details: %s\n", title, reason, link)
		var h bytes.Buffer
		_ = allClearTemplate.Execute(&h, map[string]string{"Title": title, "Reason": reason, "Link": link})
		return Message{Subject: "[RESOLVED] " + title, BodyText: text, BodyHTML: h.String()}
	default: // sms (phone recipients get an SMS: nobody wants a call to say all is well)
		text := "✅ RESOLVED: " + title
		if len([]rune(text)) > 120 {
			text = string([]rune(text)[:119]) + "…"
		}
		return Message{Subject: "[RESOLVED] " + title, BodyText: text + " (" + reason + ")"}
	}
}

// RenderDigest summarises messages held by throttling.
func (r Renderer) RenderDigest(channel string, subjects []string, window time.Duration) Message {
	n := len(subjects)
	head := fmt.Sprintf("%d more alerts in the last %s", n, humanDuration(window))
	switch channel {
	case "email":
		var t strings.Builder
		fmt.Fprintf(&t, "%s (held to avoid flooding you):\n\n", head)
		for _, s := range subjects {
			fmt.Fprintf(&t, "  • %s\n", sevPrefix.ReplaceAllString(s, ""))
		}
		fmt.Fprintf(&t, "\nOpen Kardan: %s\n", r.PublicURL)
		return Message{Subject: "[Kardan digest] " + head, BodyText: t.String()}
	default:
		var b strings.Builder
		b.WriteString("Kardan: " + head + ": ")
		for i, s := range subjects {
			s = sevPrefix.ReplaceAllString(s, "")
			if b.Len()+len(s) > 260 {
				fmt.Fprintf(&b, "… +%d", n-i)
				break
			}
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(s)
		}
		b.WriteString(" · " + r.PublicURL)
		return Message{Subject: head, BodyText: b.String()}
	}
}

var allClearTemplate = template.Must(template.New("clear").Parse(`<!doctype html>
<html><body style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;background:#f6f7f9;margin:0;padding:24px">
<table role="presentation" width="100%" style="max-width:640px;margin:auto;background:#fff;border-radius:8px;overflow:hidden;border:1px solid #e5e7eb">
<tr><td style="background:#16a34a;color:#fff;padding:12px 20px;font-size:13px;letter-spacing:.04em;text-transform:uppercase">Resolved</td></tr>
<tr><td style="padding:20px"><h1 style="font-size:20px;margin:0 0 12px;color:#111827">{{.Title}}</h1>
<p style="color:#374151">{{.Reason}}. No action needed.</p>
<p><a href="{{.Link}}" style="color:#2563eb">View the alert</a></p></td></tr></table></body></html>`))

// humanDuration renders 10m as "10 min" and 2h as "2 h".
func humanDuration(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d h", int(d/time.Hour))
	case d >= time.Minute:
		return fmt.Sprintf("%d min", int(d.Round(time.Minute)/time.Minute))
	default:
		return fmt.Sprintf("%d s", int(d.Round(time.Second)/time.Second))
	}
}
