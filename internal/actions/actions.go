// Package actions defines what runbook steps can do. A few kinds are built in
// (http_get, call_webhook, wait, escalate); every other kind is declared in
// config/actions.yaml as an HTTP call with a templated URL, so adding one is
// as easy as adding a rule. Each kind has a risk: high-risk actions always
// need a second person's approval, whatever the runbook says.
package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/tidwall/gjson"

	"github.com/undeluro/kardan-alerts/internal/tmpl"
)

const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// Definition describes one kind of action.
type Definition struct {
	Type        string   `yaml:"type" json:"type"`
	Label       string   `yaml:"label" json:"label"` // template over params: "Retry workflow {{workflowId}}"
	Description string   `yaml:"description" json:"description"`
	Risk        string   `yaml:"risk" json:"risk"`
	Params      []string `yaml:"params" json:"params"` // "name" required, "name?" optional
	Request     *Request `yaml:"request,omitempty" json:"request,omitempty"`
	Builtin     bool     `yaml:"-" json:"builtin"`

	label *tmpl.Template
	url   *tmpl.Template
}

// Request is the HTTP call a declared action makes.
type Request struct {
	Method string `yaml:"method" json:"method"`
	URL    string `yaml:"url" json:"url"`
}

// Call is one execution.
type Call struct {
	RunID       string
	Action      string
	Params      map[string]string
	AlertID     string
	AlertTitle  string
	Team        string
	RequestedBy string
	ApprovedBy  string
}

// Result of an execution.
type Result struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
}

// Builtin executes a kind implemented in code.
type Builtin func(ctx context.Context, c Call) Result

type config struct {
	AllowedHosts []string          `yaml:"allowedHosts"`
	Targets      map[string]string `yaml:"targets"`
	Actions      []Definition      `yaml:"actions"`
}

// Registry is the loaded catalogue of action kinds.
type Registry struct {
	defs     map[string]*Definition
	allowed  []string
	targets  map[string]string
	builtins map[string]Builtin
	client   *http.Client
}

// builtinDefs are always available.
var builtinDefs = []Definition{
	{Type: "http_get", Label: "Check {{url}}", Risk: RiskLow, Params: []string{"url"},
		Description: "GET a URL on an allowed host; passes on any 2xx answer (health checks)."},
	{Type: "call_webhook", Label: "Call webhook {{url}}", Risk: RiskMedium, Params: []string{"url", "method?"},
		Description: "POST the alert and the step's parameters as JSON to a URL on an allowed host."},
	{Type: "wait", Label: "Wait {{duration}}", Risk: RiskLow, Params: []string{"duration"},
		Description: "Pause the runbook before the next step (e.g. let a restart settle)."},
	{Type: "escalate", Label: "Escalate to {{to}}", Risk: RiskLow, Params: []string{"to?", "team?", "channels?"},
		Description: "Notify another team, a person or whoever is on call (to: team:x | oncall:x | user:x, or team: x)."},
}

// Load reads config/actions.yaml (a missing file leaves only the built-ins).
func Load(path string) (*Registry, error) {
	var cfg config
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	r := &Registry{defs: map[string]*Definition{}, allowed: cfg.AllowedHosts, targets: map[string]string{},
		builtins: map[string]Builtin{}, client: &http.Client{Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	for k, v := range cfg.Targets {
		r.targets[k] = strings.TrimRight(expandEnv(v), "/")
	}
	for _, d := range builtinDefs {
		d := d
		d.Builtin = true
		if err := r.add(&d); err != nil {
			return nil, err
		}
	}
	for i := range cfg.Actions {
		d := cfg.Actions[i]
		if d.Type == "" {
			return nil, fmt.Errorf("actions[%d]: type is required", i)
		}
		if _, dup := r.defs[d.Type]; dup {
			return nil, fmt.Errorf("actions[%d]: %s is already defined", i, d.Type)
		}
		if d.Request == nil || d.Request.URL == "" {
			return nil, fmt.Errorf("action %s: request.url is required", d.Type)
		}
		if err := r.add(&d); err != nil {
			return nil, err
		}
	}
	r.builtins["http_get"] = r.httpGet
	r.builtins["call_webhook"] = r.callWebhook
	return r, nil
}

func (r *Registry) add(d *Definition) error {
	switch d.Risk {
	case "":
		d.Risk = RiskMedium
	case RiskLow, RiskMedium, RiskHigh:
	default:
		return fmt.Errorf("action %s: risk must be low, medium or high", d.Type)
	}
	var err error
	if d.label, err = tmpl.Compile(d.Label); err != nil {
		return fmt.Errorf("action %s label: %w", d.Type, err)
	}
	if d.Request != nil {
		if d.Request.Method == "" {
			d.Request.Method = http.MethodPost
		}
		if d.url, err = tmpl.Compile(d.Request.URL); err != nil {
			return fmt.Errorf("action %s url: %w", d.Type, err)
		}
	}
	r.defs[d.Type] = d
	return nil
}

// Register supplies the implementation of a built-in kind that needs the
// caller's machinery (escalate needs the notification pipeline).
func (r *Registry) Register(kind string, fn Builtin) { r.builtins[kind] = fn }

func (r *Registry) Get(kind string) (*Definition, bool) {
	if r == nil {
		return nil, false
	}
	d, ok := r.defs[kind]
	return d, ok
}

// List returns the catalogue for the UI.
func (r *Registry) List() []Definition {
	out := make([]Definition, 0, len(r.defs))
	for _, d := range r.defs {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// Approval decides whether a step needs a second person. High-risk kinds
// always do, so a runbook (or text pasted into one) cannot switch it off.
func (r *Registry) Approval(kind, stepApproval string) string {
	if d, ok := r.Get(kind); ok && d.Risk == RiskHigh {
		return "required"
	}
	if stepApproval == "required" {
		return "required"
	}
	return "none"
}

// Risk of a kind ("" for unknown kinds).
func (r *Registry) Risk(kind string) string {
	if d, ok := r.Get(kind); ok {
		return d.Risk
	}
	return ""
}

// Validate checks that a step's action exists and has its required params.
func (r *Registry) Validate(kind string, params map[string]string) error {
	d, ok := r.Get(kind)
	if !ok {
		return fmt.Errorf("unknown action %q (see config/actions.yaml)", kind)
	}
	for _, p := range d.Params {
		if strings.HasSuffix(p, "?") {
			continue
		}
		if strings.TrimSpace(params[p]) == "" {
			return fmt.Errorf("action %s needs %s", kind, p)
		}
	}
	return nil
}

// Label describes a call for people: "Retry workflow WF-20261023-00042".
func (r *Registry) Label(kind string, params map[string]string) string {
	d, ok := r.Get(kind)
	if !ok {
		return kind
	}
	s := strings.TrimSpace(d.label.Render(mapResolver(params, nil)))
	if s == "" {
		return kind
	}
	return s
}

// Execute runs a call. Unknown kinds and hosts outside the allow-list fail
// without making any request.
func (r *Registry) Execute(ctx context.Context, c Call) Result {
	d, ok := r.Get(c.Action)
	if !ok {
		return Result{Output: "unknown action " + c.Action}
	}
	if fn, ok := r.builtins[c.Action]; ok {
		return fn(ctx, c)
	}
	if d.Request == nil {
		return Result{Output: c.Action + " has no implementation on this replica"}
	}
	target := d.url.Render(mapResolver(c.Params, r.targets))
	body, _ := json.Marshal(map[string]any{"action": c.Action, "params": c.Params, "alertId": c.AlertID,
		"alertTitle": c.AlertTitle, "team": c.Team, "requestedBy": c.RequestedBy, "approvedBy": c.ApprovedBy})
	return r.do(ctx, d.Request.Method, target, body, c.RunID)
}

func (r *Registry) httpGet(ctx context.Context, c Call) Result {
	return r.do(ctx, http.MethodGet, c.Params["url"], nil, c.RunID)
}

func (r *Registry) callWebhook(ctx context.Context, c Call) Result {
	method := strings.ToUpper(c.Params["method"])
	if method == "" {
		method = http.MethodPost
	}
	body, _ := json.Marshal(map[string]any{"alertId": c.AlertID, "alertTitle": c.AlertTitle, "team": c.Team,
		"params": c.Params, "requestedBy": c.RequestedBy, "approvedBy": c.ApprovedBy})
	return r.do(ctx, method, c.Params["url"], body, c.RunID)
}

// do makes the HTTP request after the allow-list check (no requests to
// arbitrary hosts: runbook text is user input, so this guards against SSRF).
func (r *Registry) do(ctx context.Context, method, target string, body []byte, idemKey string) Result {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return Result{Output: fmt.Sprintf("invalid URL %q", target)}
	}
	if !r.HostAllowed(u.Hostname()) {
		return Result{Output: fmt.Sprintf("host %s is not in allowedHosts (config/actions.yaml)", u.Hostname())}
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return Result{Output: err.Error()}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	req.Header.Set("User-Agent", "kardan-runbooks/1")
	resp, err := r.client.Do(req)
	if err != nil {
		return Result{Output: err.Error()}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	out := summarize(data)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	return Result{OK: ok, Output: fmt.Sprintf("HTTP %d %s", resp.StatusCode, out)}
}

// HostAllowed matches exact names or ".suffix" entries.
func (r *Registry) HostAllowed(host string) bool {
	host = strings.ToLower(host)
	for _, a := range r.allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == host || (strings.HasPrefix(a, ".") && strings.HasSuffix(host, a)) {
			return true
		}
	}
	return false
}

// summarize keeps a short readable answer: a message field or the start of the body.
func summarize(b []byte) string {
	if gjson.ValidBytes(b) {
		for _, k := range []string{"message", "result", "status"} {
			if v := gjson.GetBytes(b, k); v.Exists() && v.Type == gjson.String {
				return v.String()
			}
		}
	}
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// mapResolver resolves template variables from params (path-escaped when
// building URLs) and targets (base URLs, inserted raw).
func mapResolver(params, targets map[string]string) tmpl.Resolver {
	return func(path string) gjson.Result {
		if v, ok := targets[path]; ok {
			return gjson.Result{Type: gjson.String, Str: v}
		}
		if v, ok := params[path]; ok && v != "" {
			if targets != nil {
				v = url.PathEscape(v)
			}
			return gjson.Result{Type: gjson.String, Str: v}
		}
		return gjson.Result{}
	}
}

var envRef = regexp.MustCompile(`\$\{([A-Z0-9_]+)(?::-([^}]*))?\}`)

// expandEnv supports ${NAME} and ${NAME:-default}.
func expandEnv(s string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		p := envRef.FindStringSubmatch(m)
		if v := os.Getenv(p[1]); v != "" {
			return v
		}
		return p[2]
	})
}
