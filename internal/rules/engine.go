package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/undeluro/kardan-alerts/internal/schema"
)

// RuleSet is an immutable, compiled snapshot of all rules.
type RuleSet struct {
	LoadedAt time.Time   `json:"loadedAt"`
	Rules    []*Compiled `json:"-"`
	Errors   []string    `json:"errors,omitempty"` // rules that failed validation are skipped, not fatal
}

// Match returns the enabled rules the event satisfies.
func (rs *RuleSet) Match(d *schema.Doc) []*Compiled {
	var out []*Compiled
	for _, c := range rs.Rules {
		if c.Rule.IsEnabled() && c.Matches(d) {
			out = append(out, c)
		}
	}
	return out
}

// Recoveries returns the rules for which the event is a recovery signal.
func (rs *RuleSet) Recoveries(d *schema.Doc) []*Compiled {
	var out []*Compiled
	for _, c := range rs.Rules {
		if c.Rule.IsEnabled() && c.IsRecovery(d) {
			out = append(out, c)
		}
	}
	return out
}

// ByID finds a compiled rule.
func (rs *RuleSet) ByID(id string) *Compiled {
	for _, c := range rs.Rules {
		if c.Rule.ID == id {
			return c
		}
	}
	return nil
}

// Engine holds the current RuleSet. Swaps are atomic, so evaluation never
// blocks on a reload and never sees a half-updated set.
type Engine struct {
	cur atomic.Pointer[RuleSet]
}

func NewEngine() *Engine {
	e := &Engine{}
	e.cur.Store(&RuleSet{LoadedAt: time.Now()})
	return e
}

func (e *Engine) Current() *RuleSet { return e.cur.Load() }
func (e *Engine) Swap(rs *RuleSet)  { e.cur.Store(rs) }

// Build compiles rules into a RuleSet, collecting validation errors.
func Build(list []Rule, defaultWindow time.Duration) *RuleSet {
	rs := &RuleSet{LoadedAt: time.Now()}
	seen := map[string]string{}
	for _, r := range list {
		if prev, dup := seen[r.ID]; dup {
			rs.Errors = append(rs.Errors, fmt.Sprintf("rule %s (%s): duplicate id, already defined in %s", r.ID, r.Origin, prev))
			continue
		}
		c, err := Compile(r, defaultWindow)
		if err != nil {
			rs.Errors = append(rs.Errors, fmt.Sprintf("%s (%s)", err, r.Origin))
			continue
		}
		seen[r.ID] = r.Origin
		rs.Rules = append(rs.Rules, c)
	}
	sort.Slice(rs.Rules, func(i, j int) bool { return rs.Rules[i].Rule.ID < rs.Rules[j].Rule.ID })
	return rs
}

// LoadDir reads every *.yaml / *.yml file in dir. A file holds one rule, or a
// list under a top-level "rules:" key.
func LoadDir(dir string) ([]Rule, []string) {
	var out []Rule
	var errs []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("read rules dir %s: %v", dir, err)}
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		rs, err := ParseYAML(data)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		for i := range rs {
			rs[i].Origin = "file:" + name
		}
		out = append(out, rs...)
	}
	return out, errs
}

// ParseYAML accepts a single rule or {rules: [...]}.
func ParseYAML(data []byte) ([]Rule, error) {
	var list struct {
		Rules []Rule `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &list); err == nil && len(list.Rules) > 0 {
		return list.Rules, nil
	}
	var r Rule
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.ID == "" {
		return nil, fmt.Errorf("no rule found (expected an id or a rules: list)")
	}
	return []Rule{r}, nil
}
