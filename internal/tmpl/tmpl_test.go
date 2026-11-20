package tmpl

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestRender(t *testing.T) {
	doc := []byte(`{"payload":{"currency":"EUR","notional":25000000,"reason":"LIMIT_BREACH"},"name":"pricing-engine-7"}`)
	get := func(p string) gjson.Result { return gjson.GetBytes(doc, p) }

	cases := map[string]string{
		"Rejected {{payload.currency}} {{payload.notional | number}} — {{payload.reason}}": "Rejected EUR 25,000,000 — LIMIT_BREACH",
		"{{ name | upper }}":            "PRICING-ENGINE-7",
		"{{missing | default \"n/a\"}}": "n/a",
		"{{missing | default:'-'}}":     "-",
		"{{name | truncate 7}}":         "pricing…",
		"{{payload | json}}":            `{"currency":"EUR","notional":25000000,"reason":"LIMIT_BREACH"}`,
		"no variables":                  "no variables",
	}
	for src, want := range cases {
		tpl, err := Compile(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if got := tpl.Render(get); got != want {
			t.Errorf("%q: got %q, want %q", src, got, want)
		}
	}

	tpl := MustCompile("{{a}} and {{b | default x}} and {{c}}")
	_, missing := tpl.RenderMissing(get)
	if len(missing) != 2 || missing[0] != "a" || missing[1] != "c" {
		t.Errorf("missing = %v", missing)
	}
}

func TestCompileErrors(t *testing.T) {
	for _, src := range []string{"{{unclosed", "{{}}", "{{a | bogus}}"} {
		if _, err := Compile(src); err == nil {
			t.Errorf("%q: expected error", src)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	cases := map[float64]string{0: "0", 999: "999", 1000: "1,000", 25000000: "25,000,000", -1234.5: "-1,234.50"}
	for in, want := range cases {
		if got := FormatNumber(in); got != want {
			t.Errorf("%v: got %q, want %q", in, got, want)
		}
	}
}
