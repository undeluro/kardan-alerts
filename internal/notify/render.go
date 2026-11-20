// Package notify turns alerts into channel messages and delivers them from the
// outbox with retries.
package notify

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

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
		if len([]rune(text)) > 140 {
			text = string([]rune(text)[:139]) + "…"
		}
		return Message{Subject: a.Title, BodyText: text + " " + r.link(a)}
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
		fmt.Fprintf(&t, "\nOpen in Kardan: %s\n", r.link(a))

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
