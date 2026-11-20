package parse

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// Logfmt parses key=value lines: level=warn app=pricing-engine-3 msg="slow quote".
// Text before the first key=value pair (often a timestamp and level) is kept
// as "prefix", and a bare level word in it is lifted to "level".
type Logfmt struct{}

func looksLikeLogfmt(b []byte) bool {
	pairs := 0
	for _, f := range strings.Fields(string(b)) {
		if i := strings.IndexByte(f, '='); i > 0 && isKey(f[:i]) {
			pairs++
		}
	}
	return pairs >= 2
}

func isKey(s string) bool {
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return s != ""
}

func (Logfmt) Parse(raw []byte) (Result, error) {
	s := strings.TrimSpace(string(raw))
	out := map[string]any{}
	var prefix []string
	i := 0
	for i < len(s) {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		start := i
		for i < len(s) && s[i] != '=' && s[i] != ' ' {
			i++
		}
		key := s[start:i]
		if i >= len(s) || s[i] != '=' || !isKey(key) {
			if key != "" && len(out) == 0 {
				prefix = append(prefix, key)
			}
			continue
		}
		i++ // '='
		var val string
		if i < len(s) && s[i] == '"' {
			i++
			var b strings.Builder
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				b.WriteByte(s[i])
				i++
			}
			i++ // closing quote
			val = b.String()
			out[key] = val
			continue
		}
		vs := i
		for i < len(s) && s[i] != ' ' {
			i++
		}
		val = s[vs:i]
		out[key] = typed(val)
	}
	if len(out) == 0 {
		return Result{}, fmt.Errorf("no key=value pairs")
	}
	if len(prefix) > 0 {
		p := strings.Join(prefix, " ")
		out["prefix"] = p
		if _, has := out["level"]; !has {
			if m := levelRe.FindString(p); m != "" {
				out["level"] = strings.ToUpper(m)
			}
		}
	}
	b, err := json.Marshal(out)
	return Result{Payload: b, Format: "logfmt", Raw: s}, err
}
