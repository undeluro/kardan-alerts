package rules

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMergeStoredOverridesFile(t *testing.T) {
	files := []Rule{{ID: "a", Team: "x", Origin: "file:a.yaml"}, {ID: "b", Team: "x", Origin: "file:b.yaml"}}
	stored := []Stored{{Rule: Rule{ID: "b", Team: "y"}}, {Rule: Rule{ID: "c", Team: "z"}}}
	got := Merge(files, stored)
	if len(got) != 3 {
		t.Fatalf("got %d rules", len(got))
	}
	byID := map[string]Rule{}
	for _, r := range got {
		byID[r.ID] = r
	}
	if byID["b"].Team != "y" || byID["b"].Origin != "api" || byID["b"].Overrides != "file:b.yaml" {
		t.Errorf("b = %+v", byID["b"])
	}
	if byID["a"].Origin != "file:a.yaml" || byID["c"].Origin != "api" {
		t.Errorf("origins: a=%s c=%s", byID["a"].Origin, byID["c"].Origin)
	}
}

func TestYAMLRoundTripThroughJSON(t *testing.T) {
	src := []byte(`
id: rates-big-rejections
team: rates
match:
  all:
    - { field: meta.schema, op: glob, value: "trade.rejected.*" }
    - { field: payload.notional, op: gt, value: 10000000 }
    - { field: payload.reason, op: in, value: [LIMIT_BREACH, 42] }
routing:
  - { to: team:rates, channels: [email, ui] }
presentation:
  title: "Rejected {{payload.currency}} {{payload.notional | number}}"
`)
	rs, err := ParseYAML(src)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the database: JSON out, JSON in.
	b, _ := json.Marshal(rs[0])
	var back Rule
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	back.Match.Normalize()
	y, err := ToYAML(back)
	if err != nil {
		t.Fatal(err)
	}
	out := string(y)
	if strings.Contains(out, "e+07") || !strings.Contains(out, "10000000") {
		t.Errorf("numbers should stay integers:\n%s", out)
	}
	again, err := ParseYAML(y)
	if err != nil {
		t.Fatalf("re-parse: %v\n%s", err, out)
	}
	c, err := Compile(again[0], time.Minute)
	if err != nil {
		t.Fatalf("compile round-tripped rule: %v", err)
	}
	if !c.Matches(doc(t, tradeRejected)) {
		t.Errorf("round-tripped rule no longer matches:\n%s", out)
	}
}
