// Package runbooks turns instructions written in any shape into steps an
// alert can show and run. A runbook may be structured YAML (Appendix B), a
// Markdown page, a checklist, a link to a wiki page or free text copied from
// a chat; all of them parse into the same model: ordered steps that are text,
// checks or actions, with values from the alert filled in.
package runbooks

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/schema"
	"github.com/undeluro/kardan-alerts/internal/tmpl"
)

// Formats a runbook can arrive in.
const (
	FormatYAML      = "yaml"
	FormatMarkdown  = "markdown"
	FormatChecklist = "checklist"
	FormatLink      = "link"
	FormatText      = "text"
)

// Step kinds.
const (
	KindText   = "text"   // read and do (or just know)
	KindCheck  = "check"  // verify something; with an action (http_get) it checks itself
	KindAction = "action" // something Kardan can do: retry, restart, escalate...
)

// Runbook is the common model all formats parse into.
type Runbook struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Team    string           `json:"team,omitempty"`
	Format  string           `json:"format"`
	URL     string           `json:"url,omitempty"`     // link runbooks
	Summary string           `json:"summary,omitempty"` // context that is not a step ("it's usually the cache")
	Matches *rules.Condition `json:"matches,omitempty"` // attach to alerts whose event matches
	Rules   []string         `json:"rules,omitempty"`   // attach to these rules' alerts
	Schemas []string         `json:"schemas,omitempty"` // attach to events of these schemas (id or name)
	Auto    bool             `json:"auto,omitempty"`    // run steps automatically when an alert opens
	Steps   []Step           `json:"steps"`
	Notes   []string         `json:"notes,omitempty"`  // how the parser read it (shown in the preview)
	Source  string           `json:"source"`           // exactly what was written
	Origin  string           `json:"origin,omitempty"` // file:<name> or api

	match func(*schema.Doc) bool
}

// Step is one thing to do.
type Step struct {
	Kind     string            `json:"kind"`
	Text     string            `json:"text,omitempty"`     // as written; may contain {{templates}}
	Action   string            `json:"action,omitempty"`   // action type
	Params   map[string]string `json:"params,omitempty"`   // templates, filled from the alert
	Approval string            `json:"approval,omitempty"` // none | required ("" = decided by the action's risk)
	If       string            `json:"if,omitempty"`       // "retry fails twice", "latency is still high"
	Then     string            `json:"then,omitempty"`     // "resolve": close the alert when this step succeeds
	Every    string            `json:"every,omitempty"`    // repeat while the alert is open ("15m")
	Inferred bool              `json:"inferred,omitempty"` // action guessed from prose: confirm in the preview

	text   *tmpl.Template
	params map[string]*tmpl.Template
}

// Filled is a step with the alert's values in.
type Filled struct {
	Index    int               `json:"index"`
	Kind     string            `json:"kind"`
	Text     string            `json:"text"`
	Action   string            `json:"action,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	Approval string            `json:"approval,omitempty"`
	If       string            `json:"if,omitempty"`
	Then     string            `json:"then,omitempty"`
	Every    string            `json:"every,omitempty"`
	Inferred bool              `json:"inferred,omitempty"`
	Missing  []string          `json:"missing,omitempty"` // template values not found in this alert
}

// compile prepares templates and the match condition.
func (rb *Runbook) compile() error {
	if rb.ID == "" {
		return fmt.Errorf("a runbook needs an id (runbook: <id>)")
	}
	if rb.Name == "" {
		rb.Name = rb.ID
	}
	if rb.Matches != nil {
		m, err := rules.CompileCondition(*rb.Matches)
		if err != nil {
			return fmt.Errorf("matches: %w", err)
		}
		rb.match = m
	}
	for i := range rb.Steps {
		s := &rb.Steps[i]
		if s.Kind == "" {
			s.Kind = KindText
			if s.Action != "" {
				s.Kind = KindAction
			}
		}
		switch s.Approval {
		case "", "none", "required":
		default:
			return fmt.Errorf("step %d: approval must be none or required", i+1)
		}
		if s.Every != "" {
			if _, err := ParseDuration(s.Every); err != nil {
				return fmt.Errorf("step %d: every %q: %w", i+1, s.Every, err)
			}
		}
		var err error
		if s.text, err = tmpl.Compile(s.Text); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		s.params = map[string]*tmpl.Template{}
		for k, v := range s.Params {
			if s.params[k], err = tmpl.Compile(v); err != nil {
				return fmt.Errorf("step %d, %s: %w", i+1, k, err)
			}
		}
	}
	return nil
}

// Fill renders every step with values from the alert.
func (rb *Runbook) Fill(get tmpl.Resolver) []Filled {
	out := make([]Filled, len(rb.Steps))
	for i, s := range rb.Steps {
		text, missing := s.text.RenderMissing(get)
		f := Filled{Index: i, Kind: s.Kind, Text: strings.TrimSpace(text), Action: s.Action, Approval: s.Approval,
			If: s.If, Then: s.Then, Every: s.Every, Inferred: s.Inferred}
		if len(s.params) > 0 {
			f.Params = map[string]string{}
			keys := make([]string, 0, len(s.params))
			for k := range s.params {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v, m := s.params[k].RenderMissing(get)
				f.Params[k] = v
				missing = append(missing, m...)
			}
		}
		f.Missing = dedupe(missing)
		out[i] = f
	}
	return out
}

// Matches reports whether the runbook's condition matches an event.
func (rb *Runbook) MatchesDoc(d *schema.Doc) bool { return rb.match != nil && rb.match(d) }

// ParseDuration accepts Go durations ("15m") and words ("15 minutes", "2 min").
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	if m := durationRe.FindStringSubmatch(s); m != nil {
		return wordDuration(m[1], m[2]), nil
	}
	return 0, fmt.Errorf("use a duration like 15m or \"15 minutes\"")
}

// AlertVars are the "$" values every step can use besides the event's fields.
func AlertVars(id, title, team, severity, status, code string) map[string]gjson.Result {
	return map[string]gjson.Result{
		"$alertId": schema.Str(id), "$title": schema.Str(title), "$team": schema.Str(team),
		"$alertSeverity": schema.Str(severity), "$status": schema.Str(status), "$code": schema.Str(code),
	}
}

func dedupe(xs []string) []string {
	if len(xs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
