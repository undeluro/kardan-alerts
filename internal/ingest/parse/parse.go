// Package parse turns raw messages of many formats into JSON payloads, so the
// rest of the pipeline only ever deals with JSON paths.
//
// Supported formats: json, fix (tag=value), iso20022 / xml, logfmt, regex
// (named groups) and plain text. "auto" detects the format per message, which
// is what HTTP sources use by default; a source can pin a parser in
// sources.yaml.
package parse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Result is a parsed message.
type Result struct {
	Payload json.RawMessage
	Format  string
	Raw     string // original text for non-JSON formats ("" for JSON)
}

// Parser converts one raw message.
type Parser interface {
	Parse(raw []byte) (Result, error)
}

// Spec configures a parser (from sources.yaml).
type Spec struct {
	Name    string `yaml:"parser" json:"parser"`
	Pattern string `yaml:"pattern,omitempty" json:"pattern,omitempty"` // regex parser only
}

// New builds a parser by name.
func New(spec Spec) (Parser, error) {
	switch strings.ToLower(spec.Name) {
	case "", "auto":
		return Auto{}, nil
	case "json":
		return JSON{}, nil
	case "fix":
		return FIX{}, nil
	case "xml", "iso20022":
		return XML{}, nil
	case "logfmt":
		return Logfmt{}, nil
	case "text":
		return Text{}, nil
	case "regex":
		return NewRegex(spec.Pattern)
	default:
		return nil, fmt.Errorf("unknown parser %q (auto, json, fix, iso20022, xml, logfmt, regex, text)", spec.Name)
	}
}

// Auto detects the format of each message.
type Auto struct{}

func (Auto) Parse(raw []byte) (Result, error) {
	b := bytes.TrimSpace(raw)
	switch {
	case len(b) == 0:
		return Result{}, fmt.Errorf("empty message")
	case (b[0] == '{' || b[0] == '[') && json.Valid(b):
		return JSON{}.Parse(b)
	case b[0] == '<':
		if r, err := (XML{}).Parse(b); err == nil {
			return r, nil
		}
	case looksLikeFIX(b):
		if r, err := (FIX{}).Parse(b); err == nil {
			return r, nil
		}
	case looksLikeLogfmt(b):
		if r, err := (Logfmt{}).Parse(b); err == nil {
			return r, nil
		}
	}
	return Text{}.Parse(b)
}

// JSON passes valid JSON through unchanged.
type JSON struct{}

func (JSON) Parse(raw []byte) (Result, error) {
	b := bytes.TrimSpace(raw)
	if !json.Valid(b) {
		return Result{}, fmt.Errorf("invalid JSON")
	}
	return Result{Payload: json.RawMessage(b), Format: "json"}, nil
}

// Text wraps a line as {"message": ...} and lifts an obvious log level, so
// severity rules work on plain logs too.
type Text struct{}

var levelRe = regexp.MustCompile(`(?i)\b(TRACE|DEBUG|INFO|NOTICE|WARN|WARNING|ERROR|ERR|FATAL|CRITICAL|CRIT)\b`)

func (Text) Parse(raw []byte) (Result, error) {
	s := strings.TrimSpace(string(raw))
	out := map[string]any{"message": s}
	head := s
	if len(head) > 80 {
		head = head[:80]
	}
	if m := levelRe.FindString(head); m != "" {
		out["level"] = strings.ToUpper(m)
	}
	b, err := json.Marshal(out)
	return Result{Payload: b, Format: "text", Raw: s}, err
}

// Regex extracts named groups: (?P<level>\w+) becomes {"level": "..."}.
type Regex struct{ re *regexp.Regexp }

func NewRegex(pattern string) (*Regex, error) {
	if pattern == "" {
		return nil, fmt.Errorf("regex parser needs a pattern")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("regex parser: %w", err)
	}
	named := false
	for _, n := range re.SubexpNames() {
		if n != "" {
			named = true
		}
	}
	if !named {
		return nil, fmt.Errorf("regex parser pattern has no named groups like (?P<level>\\w+)")
	}
	return &Regex{re: re}, nil
}

func (r *Regex) Parse(raw []byte) (Result, error) {
	s := strings.TrimSpace(string(raw))
	m := r.re.FindStringSubmatch(s)
	if m == nil {
		// Not an error: keep the line, so nothing is silently dropped.
		res, err := Text{}.Parse(raw)
		res.Format = "regex-unmatched"
		return res, err
	}
	out := map[string]any{}
	for i, name := range r.re.SubexpNames() {
		if name != "" && i < len(m) {
			out[name] = typed(m[i])
		}
	}
	b, err := json.Marshal(out)
	return Result{Payload: b, Format: "regex", Raw: s}, err
}

var numberRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// typed turns "1240" into a number, keeps "007" or "1e5" as strings.
func typed(s string) any {
	if numberRe.MatchString(s) && len(s) < 16 {
		var f float64
		if _, err := fmt.Sscan(s, &f); err == nil {
			return json.Number(s)
		}
	}
	return s
}
