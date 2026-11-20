package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

// FieldInfo describes one leaf field of a schema.
type FieldInfo struct {
	Path      string `json:"path"`
	Type      string `json:"type"` // string | number | bool | null | array
	Example   string `json:"example,omitempty"`
	Detected  string `json:"detected,omitempty"` // finance kind, see detect.go
	Sensitive bool   `json:"sensitive,omitempty"`
}

const (
	maxDepth  = 6
	maxFields = 300
)

// idLikeKey matches map keys that are data rather than structure (ids,
// timestamps, UUIDs); they collapse to "*" so {"positions":{"T-1":..}} and
// {"positions":{"T-2":..}} share one signature.
var idLikeKey = regexp.MustCompile(`\d{4,}|^[0-9a-fA-F-]{16,}$|^\d+$`)

// Signature is a stable hash of a payload's structure (paths and types), not
// its values. Events with the same signature share a schema.
func Signature(payload []byte) string {
	var keys []string
	walk(gjson.ParseBytes(payload), "", 0, func(path string, r gjson.Result) bool {
		keys = append(keys, path+":"+typeName(r))
		return len(keys) < maxFields
	}, true)
	sort.Strings(keys)
	h := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return hex.EncodeToString(h[:8])
}

// Flatten lists leaf fields with an example value and finance detection.
func Flatten(payload []byte) []FieldInfo {
	var out []FieldInfo
	seen := map[string]bool{}
	walk(gjson.ParseBytes(payload), "", 0, func(path string, r gjson.Result) bool {
		if seen[path] {
			return true
		}
		seen[path] = true
		f := FieldInfo{Path: path, Type: typeName(r)}
		if r.Type != gjson.JSON {
			f.Example = truncate(r.String(), 80)
		} else if r.IsArray() {
			f.Example = truncate(r.Raw, 80)
		}
		f.Detected, f.Sensitive = Detect(path, r)
		out = append(out, f)
		return len(out) < maxFields
	}, false)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// walk visits leaves. Arrays of scalars are leaves (rules match any element);
// arrays of objects are described by their first element as path.0.field.
func walk(r gjson.Result, prefix string, depth int, visit func(string, gjson.Result) bool, normalize bool) bool {
	if r.IsObject() && depth < maxDepth {
		ok := true
		r.ForEach(func(k, v gjson.Result) bool {
			key := escapeKey(k.String())
			if normalize && idLikeKey.MatchString(k.String()) {
				key = "*"
			}
			ok = walk(v, join(prefix, key), depth+1, visit, normalize)
			return ok
		})
		return ok
	}
	if r.IsArray() && depth < maxDepth {
		first := r.Get("0")
		if first.IsObject() {
			idx := "0"
			if normalize {
				idx = "[]"
			}
			return walk(first, join(prefix, idx), depth+1, visit, normalize)
		}
	}
	if prefix == "" {
		return true
	}
	return visit(prefix, r)
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// escapeKey makes a JSON key safe as a gjson path segment.
func escapeKey(k string) string {
	if !strings.ContainsAny(k, ".*?#|@\\") {
		return k
	}
	var b strings.Builder
	for _, r := range k {
		if strings.ContainsRune(".*?#|@\\", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func typeName(r gjson.Result) string {
	switch r.Type {
	case gjson.String:
		return "string"
	case gjson.Number:
		return "number"
	case gjson.True, gjson.False:
		return "bool"
	case gjson.Null:
		return "null"
	default:
		if r.IsArray() {
			return "array"
		}
		return "object"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
