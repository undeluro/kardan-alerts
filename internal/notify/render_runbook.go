package notify

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/undeluro/kardan-alerts/internal/alerting"
)

// RunbookSummary is what a message says about the alert's runbook: its name
// and the next steps with the alert's values filled in (and masked for the
// channel like the rest of the message).
type RunbookSummary struct {
	Name  string
	URL   string // link runbooks
	Steps []RunbookLine
}

// RunbookLine is one step as a person reads it.
type RunbookLine struct {
	Text     string
	Action   bool   // Kardan can run it from the alert page
	Approval bool   // needs a second person
	If       string // condition
}

// RenderWithRunbooks renders an alert and adds what to do next: the full
// steps in email, a pointer and the link in SMS, the first step in a call.
func (r Renderer) RenderWithRunbooks(channel string, a *alerting.Alert, books []RunbookSummary) Message {
	m := r.Render(channel, a)
	if len(books) == 0 {
		return m
	}
	link := r.link(a) + "#runbook"
	switch channel {
	case "sms":
		m.BodyText += fmt.Sprintf(" · Runbook: %s (%d steps) %s", books[0].Name, len(books[0].Steps), link)
	case "phone":
		if len(books[0].Steps) > 0 {
			first := strings.TrimRight(books[0].Steps[0].Text, ".")
			m.BodyText = strings.TrimSuffix(m.BodyText, " Press 1 to acknowledge.") +
				fmt.Sprintf(" Runbook %s. First step: %s. Press 1 to acknowledge.", books[0].Name, first)
		}
	default: // email: every step, numbered, with conditions and approvals
		var t strings.Builder
		for _, b := range books {
			fmt.Fprintf(&t, "\nWhat to do next (runbook %s):\n", b.Name)
			if b.URL != "" {
				fmt.Fprintf(&t, "  %s\n", b.URL)
			}
			for i, s := range b.Steps {
				fmt.Fprintf(&t, "  %d. %s%s\n", i+1, ifPrefix(s.If), s.Text)
				if s.Action {
					note := "Kardan can run this from the alert page"
					if s.Approval {
						note += " after a second person approves"
					}
					fmt.Fprintf(&t, "     (%s)\n", note)
				}
			}
		}
		fmt.Fprintf(&t, "\nRun the steps: %s\n", link)
		m.BodyText = strings.Replace(m.BodyText, "\nOpen in Kardan:", t.String()+"\nOpen in Kardan:", 1)
		var h bytes.Buffer
		_ = runbookHTML.Execute(&h, map[string]any{"Books": books, "Link": link})
		m.BodyHTML = strings.Replace(m.BodyHTML, `<p style="margin-top:20px">`, h.String()+`<p style="margin-top:20px">`, 1)
	}
	return m
}

func ifPrefix(cond string) string {
	if cond == "" {
		return ""
	}
	return "If " + cond + ": "
}

var runbookHTML = template.Must(template.New("rb").Funcs(template.FuncMap{"inc": func(i int) int { return i + 1 }}).Parse(`
{{range .Books}}<div style="margin:16px 0;padding:12px 16px;background:#f9fafb;border:1px solid #e5e7eb;border-radius:6px">
<div style="font-size:12px;color:#6b7280;text-transform:uppercase;letter-spacing:.04em">What to do next · runbook {{.Name}}</div>
{{if .URL}}<p><a href="{{.URL}}">{{.URL}}</a></p>{{end}}
<ol style="margin:8px 0 0;padding-left:20px;color:#111827;font-size:14px">
{{range .Steps}}<li style="margin:4px 0">{{if .If}}<i>If {{.If}}:</i> {{end}}{{.Text}}{{if .Action}} <span style="font-size:12px;color:#6b7280">({{if .Approval}}needs approval · {{end}}one click in Kardan)</span>{{end}}</li>
{{end}}</ol></div>{{end}}`))

// Approval is a request for a second person to approve a risky step.
type Approval struct {
	Label       string
	AlertTitle  string
	AlertID     string
	Risk        string
	RequestedBy string
	Code        string // APPROVE <code> by SMS
	ApproveURL  string // signed one-click links for email
	RejectURL   string
}

// RenderApproval asks someone to approve a step: by SMS reply, or with the
// signed links in an email.
func (r Renderer) RenderApproval(channel string, ap Approval) Message {
	subject := fmt.Sprintf("Approval needed: %s", ap.Label)
	switch channel {
	case "sms":
		text := fmt.Sprintf("✋ Kardan approval (four-eyes): %s — for \"%s\", requested by %s. Reply APPROVE %s or REJECT %s",
			ap.Label, truncate(ap.AlertTitle, 60), ap.RequestedBy, ap.Code, ap.Code)
		return Message{Subject: subject, BodyText: text}
	default:
		text := fmt.Sprintf("%s\n\nAlert: %s\nRisk: %s\nRequested by: %s\n\nApprove: %s\nReject:  %s\n\nOr reply APPROVE %s to the SMS. "+
			"You cannot approve a step you requested yourself.\nAlert: %s/alerts/%s\n",
			subject, ap.AlertTitle, ap.Risk, ap.RequestedBy, ap.ApproveURL, ap.RejectURL, ap.Code, r.PublicURL, ap.AlertID)
		var h bytes.Buffer
		_ = approvalHTML.Execute(&h, map[string]any{"A": ap, "Link": fmt.Sprintf("%s/alerts/%s", r.PublicURL, ap.AlertID)})
		return Message{Subject: "[APPROVAL] " + ap.Label, BodyText: text, BodyHTML: h.String()}
	}
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

var approvalHTML = template.Must(template.New("approval").Parse(`<!doctype html>
<html><body style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;background:#f6f7f9;margin:0;padding:24px">
<table role="presentation" width="100%" style="max-width:640px;margin:auto;background:#fff;border-radius:8px;overflow:hidden;border:1px solid #e5e7eb">
<tr><td style="background:#a21caf;color:#fff;padding:12px 20px;font-size:13px;letter-spacing:.04em;text-transform:uppercase">Approval needed · four-eyes · {{.A.Risk}} risk</td></tr>
<tr><td style="padding:20px">
<h1 style="font-size:20px;margin:0 0 12px;color:#111827">{{.A.Label}}</h1>
<p style="color:#374151">For the alert <a href="{{.Link}}">{{.A.AlertTitle}}</a>, requested by <b>{{.A.RequestedBy}}</b>.</p>
<p style="margin-top:20px">
<a href="{{.A.ApproveURL}}" style="background:#16a34a;color:#fff;padding:10px 16px;border-radius:6px;text-decoration:none;margin-right:8px">Approve</a>
<a href="{{.A.RejectURL}}" style="background:#fff;color:#b91c1c;padding:9px 15px;border-radius:6px;text-decoration:none;border:1px solid #fecaca">Reject</a></p>
<p style="color:#6b7280;font-size:12px">Or reply APPROVE {{.A.Code}} to the SMS. The link opens a confirmation page; it works once and expires in 24 hours.</p>
</td></tr></table></body></html>`))
