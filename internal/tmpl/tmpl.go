// Package tmpl renders Mustache-style templates such as
//
//	Rejected {{payload.currency}} {{payload.notional | number}} — {{payload.reason}}
//
// Variables are field paths (or "$facets") resolved against an event, using
// the same syntax as rule conditions. Templates contain no logic, so a rule
// author cannot run code through them. Output is plain text; callers escape it
// for HTML.
package tmpl

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// Resolver looks up a path for the event being rendered.
type Resolver func(path string) gjson.Result

type filter struct {
	name string
	arg  string
}

type part struct {
	literal string
	path    string // empty for literal parts
	filters []filter
}

type Template struct {
	src   string
	parts []part
}

var knownFilters = map[string]bool{
	"number": true, "upper": true, "lower": true, "default": true, "truncate": true, "json": true,
}

// Compile parses src once; Render is then allocation-light.
func Compile(src string) (*Template, error) {
	t := &Template{src: src}
	rest := src
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			if rest != "" {
				t.parts = append(t.parts, part{literal: rest})
			}
			return t, nil
		}
		end := strings.Index(rest[start:], "}}")
		if end < 0 {
			return nil, fmt.Errorf("template %q: unclosed {{", src)
		}
		if start > 0 {
			t.parts = append(t.parts, part{literal: rest[:start]})
		}
		expr := strings.TrimSpace(rest[start+2 : start+end])
		p, err := parseExpr(expr)
		if err != nil {
			return nil, fmt.Errorf("template %q: %w", src, err)
		}
		t.parts = append(t.parts, p)
		rest = rest[start+end+2:]
	}
}

// MustCompile panics on error; for constants only.
func MustCompile(src string) *Template {
	t, err := Compile(src)
	if err != nil {
		panic(err)
	}
	return t
}

func parseExpr(expr string) (part, error) {
	segs := strings.Split(expr, "|")
	p := part{path: strings.TrimSpace(segs[0])}
	if p.path == "" {
		return part{}, fmt.Errorf("empty variable in {{%s}}", expr)
	}
	for _, s := range segs[1:] {
		s = strings.TrimSpace(s)
		name, arg := s, ""
		if i := strings.IndexAny(s, " :"); i >= 0 {
			name, arg = s[:i], strings.TrimSpace(s[i+1:])
			arg = strings.Trim(arg, `"'`)
		}
		if !knownFilters[name] {
			return part{}, fmt.Errorf("unknown filter %q (known: number, upper, lower, default, truncate, json)", name)
		}
		p.filters = append(p.filters, filter{name: name, arg: arg})
	}
	return p, nil
}

func (t *Template) String() string { return t.src }

// Vars returns the paths the template references.
func (t *Template) Vars() []string {
	var out []string
	for _, p := range t.parts {
		if p.path != "" {
			out = append(out, p.path)
		}
	}
	return out
}

// Render fills the template. Missing variables render as "" (or the default
// filter's value).
func (t *Template) Render(get Resolver) string {
	s, _ := t.RenderMissing(get)
	return s
}

// RenderMissing also reports which variables could not be resolved, so the UI
// can highlight them in previews.
func (t *Template) RenderMissing(get Resolver) (string, []string) {
	if t == nil {
		return "", nil
	}
	var b strings.Builder
	var missing []string
	for _, p := range t.parts {
		if p.path == "" {
			b.WriteString(p.literal)
			continue
		}
		r := get(p.path)
		val := ""
		if r.Exists() {
			val = scalar(r)
		}
		hadDefault := false
		for _, f := range p.filters {
			val = apply(f, r, val)
			if f.name == "default" {
				hadDefault = true
			}
		}
		if !r.Exists() && !hadDefault {
			missing = append(missing, p.path)
		}
		b.WriteString(val)
	}
	return b.String(), missing
}

func scalar(r gjson.Result) string {
	switch r.Type {
	case gjson.String:
		return r.Str
	case gjson.Number:
		return r.Raw
	case gjson.True:
		return "true"
	case gjson.False:
		return "false"
	case gjson.Null:
		return ""
	default:
		return r.Raw
	}
}

func apply(f filter, r gjson.Result, val string) string {
	switch f.name {
	case "number":
		if num, err := strconv.ParseFloat(val, 64); err == nil {
			return FormatNumber(num)
		}
		return val
	case "upper":
		return strings.ToUpper(val)
	case "lower":
		return strings.ToLower(val)
	case "default":
		if val == "" {
			return f.arg
		}
		return val
	case "truncate":
		n, err := strconv.Atoi(f.arg)
		if err != nil || n <= 0 {
			return val
		}
		runes := []rune(val)
		if len(runes) <= n {
			return val
		}
		return string(runes[:n]) + "…"
	case "json":
		if r.Exists() {
			return r.Raw
		}
		return "null"
	}
	return val
}

// FormatNumber renders 25000000 as "25,000,000" and 1234.5 as "1,234.50".
func FormatNumber(n float64) string {
	neg := n < 0
	n = math.Abs(n)
	whole := math.Floor(n)
	frac := n - whole
	digits := strconv.FormatFloat(whole, 'f', 0, 64)
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if frac > 0.000001 {
		b.WriteString(strconv.FormatFloat(frac, 'f', 2, 64)[1:])
	}
	return b.String()
}
