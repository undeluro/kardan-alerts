package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/tidwall/gjson"

	"github.com/undeluro/kardan-alerts/internal/schema"
	"github.com/undeluro/kardan-alerts/internal/tmpl"
)

type predicate func(d *schema.Doc) bool

// Field is one label/path pair shown on an alert card.
type Field struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	// format renders "path | number" style fields; nil shows the raw value.
	format *tmpl.Template
}

// Compiled is a rule turned into closures: paths are parsed, regexes and
// globs are built and templates are tokenised once per rule version, so
// evaluating an event costs a few gjson lookups.
type Compiled struct {
	Rule     Rule
	match    predicate
	title    *tmpl.Template
	body     *tmpl.Template
	severity *tmpl.Template
	dedupKey *tmpl.Template
	Fields   []Field
	Window   time.Duration

	groupBy      []string
	groupWindow  time.Duration
	recovery     predicate     // autoResolve.when
	ResolveAfter time.Duration // autoResolve.after (0 = never)

	timeAware
}

var defaultTitle = tmpl.MustCompile("{{$type | default \"event\"}} from {{$service | default \"unknown service\"}}")
var defaultSeverity = tmpl.MustCompile("{{$severity}}")

// Ops lists the supported operators, for validation messages and the UI.
var Ops = []string{"eq", "neq", "in", "nin", "gt", "gte", "lt", "lte", "contains", "startsWith",
	"endsWith", "glob", "regex", "exists", "severityAtLeast"}

// Compile validates a rule and builds its predicate and templates.
func Compile(r Rule, defaultWindow time.Duration) (*Compiled, error) {
	if strings.TrimSpace(r.ID) == "" {
		return nil, fmt.Errorf("rule has no id")
	}
	wrap := func(err error) error { return fmt.Errorf("rule %s: %w", r.ID, err) }
	if strings.TrimSpace(r.Team) == "" {
		return nil, wrap(fmt.Errorf("team is required"))
	}
	m, err := compileCond(r.Match, "match")
	if err != nil {
		return nil, wrap(err)
	}
	c := &Compiled{Rule: r, match: m, Window: defaultWindow}

	if c.title, err = optionalTemplate(r.Presentation.Title, defaultTitle); err != nil {
		return nil, wrap(fmt.Errorf("presentation.title: %w", err))
	}
	if r.Presentation.Body != "" {
		if c.body, err = tmpl.Compile(r.Presentation.Body); err != nil {
			return nil, wrap(fmt.Errorf("presentation.body: %w", err))
		}
	}
	if c.severity, err = optionalTemplate(r.Presentation.Severity, defaultSeverity); err != nil {
		return nil, wrap(fmt.Errorf("presentation.severity: %w", err))
	}
	if r.Dedup.Key != "" {
		if c.dedupKey, err = tmpl.Compile(r.Dedup.Key); err != nil {
			return nil, wrap(fmt.Errorf("dedup.key: %w", err))
		}
	}
	if r.Dedup.Window != "" {
		w, err := time.ParseDuration(r.Dedup.Window)
		if err != nil || w <= 0 {
			return nil, wrap(fmt.Errorf("dedup.window %q is not a positive duration like 5m", r.Dedup.Window))
		}
		c.Window = w
	}
	for _, p := range r.Presentation.Fields {
		f, err := parseField(p)
		if err != nil {
			return nil, wrap(fmt.Errorf("presentation.fields %q: %w", p, err))
		}
		c.Fields = append(c.Fields, f)
	}
	if g := r.Group; g != nil {
		if len(g.By) == 0 {
			return nil, wrap(fmt.Errorf("group.by needs at least one field"))
		}
		w, err := time.ParseDuration(g.Window)
		if err != nil || w < time.Second || w > 24*time.Hour {
			return nil, wrap(fmt.Errorf("group.window %q must be a duration between 1s and 24h, like 5m", g.Window))
		}
		c.groupBy, c.groupWindow = g.By, w
	}
	if ar := r.AutoResolve; ar != nil {
		if ar.When == nil && ar.After == "" {
			return nil, wrap(fmt.Errorf("autoResolve needs when (a recovery condition) and/or after (a duration)"))
		}
		if ar.When != nil {
			if c.groupWindow > 0 {
				return nil, wrap(fmt.Errorf("autoResolve.when cannot be combined with group: windowed alerts close on their own"))
			}
			if c.recovery, err = compileCond(*ar.When, "autoResolve.when"); err != nil {
				return nil, wrap(err)
			}
		}
		if ar.After != "" {
			d, err := time.ParseDuration(ar.After)
			if err != nil || d < time.Second {
				return nil, wrap(fmt.Errorf("autoResolve.after %q must be a positive duration like 30m", ar.After))
			}
			c.ResolveAfter = d
		}
	}
	if err := c.compileTimeAware(r); err != nil {
		return nil, wrap(err)
	}
	for i, rt := range r.Routing {
		if !strings.HasPrefix(rt.To, "team:") && !strings.HasPrefix(rt.To, "user:") && !strings.HasPrefix(rt.To, "oncall:") {
			return nil, wrap(fmt.Errorf("routing[%d].to %q must start with team:, oncall: or user:", i, rt.To))
		}
		if len(rt.Channels) == 0 {
			return nil, wrap(fmt.Errorf("routing[%d]: channels are required (email, sms, phone, ui, preferences)", i))
		}
		for _, ch := range rt.Channels {
			switch ch {
			case "email", "sms", "phone", "ui", "preferences":
			default:
				return nil, wrap(fmt.Errorf("routing[%d]: unknown channel %q (email, sms, phone, ui, preferences)", i, ch))
			}
		}
	}
	return c, nil
}

func optionalTemplate(src string, def *tmpl.Template) (*tmpl.Template, error) {
	if strings.TrimSpace(src) == "" {
		return def, nil
	}
	return tmpl.Compile(src)
}

// parseField accepts "path", "Label=path" and either with filters:
// "Rejected USD=$sum | number".
func parseField(spec string) (Field, error) {
	f := Field{Path: spec}
	if label, path, ok := strings.Cut(spec, "="); ok {
		f.Label, f.Path = strings.TrimSpace(label), strings.TrimSpace(path)
	}
	if path, _, ok := strings.Cut(f.Path, "|"); ok {
		t, err := tmpl.Compile("{{" + f.Path + "}}")
		if err != nil {
			return f, err
		}
		f.Path, f.format = strings.TrimSpace(path), t
	}
	if f.Label == "" {
		f.Label = humanize(f.Path)
	}
	return f, nil
}

// humanize turns "payload.tradeId" into "Trade Id" and "error.message" into "Message".
func humanize(path string) string {
	last := path
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		last = path[i+1:]
	}
	last = strings.TrimLeft(last, `$\@#`) // facets, escaped XML attributes (\@Ccy) and text nodes
	var b strings.Builder
	for i, r := range last {
		switch {
		case r == '_' || r == '-':
			b.WriteByte(' ')
			continue
		case i == 0:
			b.WriteRune(unicode.ToUpper(r))
			continue
		case unicode.IsUpper(r):
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Matches reports whether the event satisfies the rule's condition.
func (c *Compiled) Matches(d *schema.Doc) bool { return c.match(d) }

// Rendered is the presentation of an alert for one event.
type Rendered struct {
	Title    string          `json:"title"`
	Body     string          `json:"body"`
	Severity string          `json:"severity"`
	Color    string          `json:"color"`
	Fields   []RenderedField `json:"fields"`
}

type RenderedField struct {
	Label     string `json:"label"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive,omitempty"` // masked on external channels
}

// Render applies the rule's presentation to an event, unmasked.
func (c *Compiled) Render(d *schema.Doc) Rendered { return c.RenderWith(d.Get, nil) }

// RenderWith renders through a resolver, which is how channel-specific
// masking is applied: the same rule produces the full alert for the UI and a
// redacted one for SMS. sensitive, if set, flags fields to show with a lock.
func (c *Compiled) RenderWith(get tmpl.Resolver, sensitive func(path string) bool) Rendered {
	out := Rendered{
		Title: strings.TrimSpace(c.title.Render(get)),
		Body:  strings.TrimSpace(c.body.Render(get)),
	}
	out.Severity = schema.NormalizeSeverity(c.severity.Render(get))
	if out.Severity == "" {
		out.Severity = "warning"
	}
	out.Color = c.Rule.Presentation.Color
	if out.Color == "" {
		out.Color = colorFor(out.Severity)
	}
	for _, f := range c.Fields {
		r := get(f.Path)
		if !r.Exists() {
			continue
		}
		v := r.String()
		if r.IsObject() || r.IsArray() {
			v = r.Raw
		}
		if f.format != nil {
			v = f.format.Render(get)
		}
		rf := RenderedField{Label: f.Label, Value: v}
		if sensitive != nil && sensitive(f.Path) {
			rf.Sensitive = true
		}
		out.Fields = append(out.Fields, rf)
	}
	return out
}

// ColorFor is the default colour for a severity.
func ColorFor(sev string) string { return colorFor(sev) }

func colorFor(sev string) string {
	switch sev {
	case "critical":
		return "red"
	case "error":
		return "orange"
	case "warning":
		return "amber"
	case "info":
		return "blue"
	default:
		return "gray"
	}
}

// defaultDedupPaths identify "the same problem" when a rule sets no dedup key.
var defaultDedupPaths = []string{"$type", "$service", "$env", "error.code", "alertname", "reason",
	"payload.reason", "code", "step"}

// Fingerprint identifies the problem an event represents for this rule.
// Events with the same fingerprint inside the dedup window become one alert.
func (c *Compiled) Fingerprint(d *schema.Doc) string {
	h := sha256.New()
	h.Write([]byte(c.Rule.ID))
	h.Write([]byte{0})
	if c.groupWindow > 0 {
		for _, p := range c.groupBy {
			h.Write([]byte(d.Get(p).String()))
			h.Write([]byte{0})
		}
		start, _ := c.WindowFor(d.Env.ReceivedAt)
		h.Write([]byte(start.UTC().Format(time.RFC3339)))
		return hex.EncodeToString(h.Sum(nil))[:32]
	}
	if c.dedupKey != nil {
		h.Write([]byte(c.dedupKey.Render(d.Get)))
	} else {
		for _, p := range defaultDedupPaths {
			h.Write([]byte(d.Get(p).String()))
			h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// Grouped reports whether the rule makes windowed digest alerts.
func (c *Compiled) Grouped() bool { return c.groupWindow > 0 }

// WindowFor returns the tumbling window containing t (aligned to the window
// size, so every replica computes the same boundaries).
func (c *Compiled) WindowFor(t time.Time) (start, end time.Time) {
	if c.groupWindow <= 0 {
		return time.Time{}, time.Time{}
	}
	start = t.UTC().Truncate(c.groupWindow)
	return start, start.Add(c.groupWindow)
}

// IsRecovery reports whether the event is a recovery signal for this rule
// (autoResolve.when): it resolves the open alert with the same fingerprint
// instead of counting as an occurrence.
func (c *Compiled) IsRecovery(d *schema.Doc) bool { return c.recovery != nil && c.recovery(d) }

// ---- condition compiler ----------------------------------------------------

func compileCond(c Condition, at string) (predicate, error) {
	kinds := 0
	if len(c.All) > 0 {
		kinds++
	}
	if len(c.Any) > 0 {
		kinds++
	}
	if c.Not != nil {
		kinds++
	}
	if c.Field != "" || c.Op != "" {
		kinds++
	}
	switch {
	case kinds == 0:
		return nil, fmt.Errorf("%s: empty condition (use all/any/not or field+op)", at)
	case kinds > 1:
		return nil, fmt.Errorf("%s: a condition must be exactly one of all, any, not, or field+op", at)
	}

	switch {
	case len(c.All) > 0:
		ps, err := compileList(c.All, at+".all")
		if err != nil {
			return nil, err
		}
		return func(d *schema.Doc) bool {
			for _, p := range ps {
				if !p(d) {
					return false
				}
			}
			return true
		}, nil
	case len(c.Any) > 0:
		ps, err := compileList(c.Any, at+".any")
		if err != nil {
			return nil, err
		}
		return func(d *schema.Doc) bool {
			for _, p := range ps {
				if p(d) {
					return true
				}
			}
			return false
		}, nil
	case c.Not != nil:
		p, err := compileCond(*c.Not, at+".not")
		if err != nil {
			return nil, err
		}
		return func(d *schema.Doc) bool { return !p(d) }, nil
	default:
		return compileLeaf(c, at)
	}
}

func compileList(cs []Condition, at string) ([]predicate, error) {
	out := make([]predicate, len(cs))
	for i, c := range cs {
		p, err := compileCond(c, fmt.Sprintf("%s[%d]", at, i))
		if err != nil {
			return nil, err
		}
		out[i] = p
	}
	return out, nil
}

func compileLeaf(c Condition, at string) (predicate, error) {
	field := strings.TrimSpace(c.Field)
	if field == "" {
		return nil, fmt.Errorf("%s: field is required", at)
	}
	at = fmt.Sprintf("%s (%s %s)", at, field, c.Op)
	get := func(d *schema.Doc) gjson.Result { return d.Get(field) }

	switch c.Op {
	case "eq", "neq":
		want := c.Value
		eq := func(d *schema.Doc) bool { return anyElem(get(d), func(r gjson.Result) bool { return equal(r, want) }) }
		if c.Op == "neq" {
			return not(eq), nil
		}
		return eq, nil

	case "in", "nin":
		list, ok := asList(c.Value)
		if !ok {
			return nil, fmt.Errorf("%s: value must be a list", at)
		}
		in := func(d *schema.Doc) bool {
			return anyElem(get(d), func(r gjson.Result) bool {
				for _, w := range list {
					if equal(r, w) {
						return true
					}
				}
				return false
			})
		}
		if c.Op == "nin" {
			return not(in), nil
		}
		return in, nil

	case "gt", "gte", "lt", "lte":
		want, ok := toFloat(c.Value)
		if !ok {
			return nil, fmt.Errorf("%s: value must be a number", at)
		}
		cmp := map[string]func(a, b float64) bool{
			"gt":  func(a, b float64) bool { return a > b },
			"gte": func(a, b float64) bool { return a >= b },
			"lt":  func(a, b float64) bool { return a < b },
			"lte": func(a, b float64) bool { return a <= b },
		}[c.Op]
		return func(d *schema.Doc) bool {
			got, ok := resultFloat(get(d))
			return ok && cmp(got, want)
		}, nil

	case "contains":
		needle := strings.ToLower(valueString(c.Value))
		return func(d *schema.Doc) bool {
			r := get(d)
			if r.IsArray() {
				return anyElem(r, func(e gjson.Result) bool { return equal(e, c.Value) })
			}
			return r.Exists() && strings.Contains(strings.ToLower(r.String()), needle)
		}, nil

	case "startsWith", "endsWith":
		affix := valueString(c.Value)
		fn := strings.HasPrefix
		if c.Op == "endsWith" {
			fn = strings.HasSuffix
		}
		return func(d *schema.Doc) bool {
			r := get(d)
			return r.Exists() && fn(r.String(), affix)
		}, nil

	case "glob":
		re, err := globToRegexp(valueString(c.Value))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", at, err)
		}
		return func(d *schema.Doc) bool {
			return anyElem(get(d), func(r gjson.Result) bool { return r.Exists() && re.MatchString(r.String()) })
		}, nil

	case "regex":
		re, err := regexp.Compile(valueString(c.Value))
		if err != nil {
			return nil, fmt.Errorf("%s: invalid regex: %w", at, err)
		}
		return func(d *schema.Doc) bool {
			return anyElem(get(d), func(r gjson.Result) bool { return r.Exists() && re.MatchString(r.String()) })
		}, nil

	case "exists":
		want := true
		if b, ok := c.Value.(bool); ok {
			want = b
		}
		return func(d *schema.Doc) bool { return get(d).Exists() == want }, nil

	case "severityAtLeast":
		min := schema.ParseLevel(valueString(c.Value))
		if min == schema.LevelUnknown {
			return nil, fmt.Errorf("%s: unknown severity %q (critical, error, warning, info, debug)", at, valueString(c.Value))
		}
		return func(d *schema.Doc) bool {
			r := get(d)
			return r.Exists() && schema.ParseLevel(r.String()) >= min
		}, nil

	case "":
		return nil, fmt.Errorf("%s: op is required (%s)", at, strings.Join(Ops, ", "))
	default:
		return nil, fmt.Errorf("%s: unknown op %q (%s)", at, c.Op, strings.Join(Ops, ", "))
	}
}

func not(p predicate) predicate { return func(d *schema.Doc) bool { return !p(d) } }

// anyElem applies fn to each element of an array result, or to the result itself.
func anyElem(r gjson.Result, fn func(gjson.Result) bool) bool {
	if r.IsArray() {
		found := false
		r.ForEach(func(_, v gjson.Result) bool {
			if fn(v) {
				found = true
				return false
			}
			return true
		})
		return found
	}
	return fn(r)
}

func equal(r gjson.Result, want any) bool {
	if !r.Exists() {
		return false
	}
	if wf, ok := toFloat(want); ok {
		if rf, ok := resultFloat(r); ok {
			return rf == wf
		}
	}
	if wb, ok := want.(bool); ok {
		return (r.Type == gjson.True && wb) || (r.Type == gjson.False && !wb)
	}
	return r.String() == valueString(want)
}

func resultFloat(r gjson.Result) (float64, bool) {
	switch r.Type {
	case gjson.Number:
		return r.Num, true
	case gjson.String:
		f, err := strconv.ParseFloat(strings.TrimSpace(r.Str), 64)
		return f, err == nil
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(n), "_", ""), 64)
		return f, err == nil
	}
	return 0, false
}

func valueString(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	default:
		return fmt.Sprint(s)
	}
}

func asList(v any) ([]any, bool) {
	switch l := v.(type) {
	case []any:
		return l, true
	case []string:
		out := make([]any, len(l))
		for i, s := range l {
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

// globToRegexp converts "trade.rejected.*" / "pricing-engine-*" into an
// anchored, case-insensitive regexp. Only * and ? are special.
func globToRegexp(glob string) (*regexp.Regexp, error) {
	if glob == "" {
		return nil, fmt.Errorf("glob pattern is empty")
	}
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range glob {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// ReferencedFields returns every field path a condition reads (for the UI and
// for schema-aware validation).
func ReferencedFields(c Condition) []string {
	set := map[string]bool{}
	var walk func(Condition)
	walk = func(c Condition) {
		if c.Field != "" {
			set[c.Field] = true
		}
		for _, x := range c.All {
			walk(x)
		}
		for _, x := range c.Any {
			walk(x)
		}
		if c.Not != nil {
			walk(*c.Not)
		}
	}
	walk(c)
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// CompileCondition compiles a condition tree on its own (runbook matches).
func CompileCondition(c Condition) (func(d *schema.Doc) bool, error) {
	c.Normalize()
	p, err := compileCond(c, "matches")
	if err != nil {
		return nil, err
	}
	return p, nil
}
