// Package rules holds the rule model, the compiler that turns rules into fast
// predicates, and the engine that evaluates them against events.
package rules

// Rule decides which events reach a team, who is told, on which channels, and
// how the resulting alert looks. Rules are data (YAML files or API/UI edits),
// never code.
type Rule struct {
	ID           string       `yaml:"id" json:"id"`
	Name         string       `yaml:"name,omitempty" json:"name,omitempty"`
	Description  string       `yaml:"description,omitempty" json:"description,omitempty"`
	Team         string       `yaml:"team" json:"team"`
	Enabled      *bool        `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Match        Condition    `yaml:"match" json:"match"`
	Routing      []Route      `yaml:"routing,omitempty" json:"routing,omitempty"`
	Presentation Presentation `yaml:"presentation,omitempty" json:"presentation,omitempty"`
	Dedup        Dedup        `yaml:"dedup,omitempty" json:"dedup,omitempty"`
	Group        *Group       `yaml:"group,omitempty" json:"group,omitempty"`
	AutoResolve  *AutoResolve `yaml:"autoResolve,omitempty" json:"autoResolve,omitempty"`
	Runbooks     []string     `yaml:"runbooks,omitempty" json:"runbooks,omitempty"`

	// Origin records where the rule came from ("file:<name>" or "api").
	Origin string `yaml:"-" json:"origin,omitempty"`
	// Overrides is set when an API rule replaces a file rule with the same id.
	Overrides string `yaml:"-" json:"overrides,omitempty"`
}

// IsEnabled defaults to true when the field is omitted.
func (r Rule) IsEnabled() bool { return r.Enabled == nil || *r.Enabled }

// Condition is a tree: exactly one of All, Any, Not, or a leaf (Field + Op).
type Condition struct {
	All []Condition `yaml:"all,omitempty" json:"all,omitempty"`
	Any []Condition `yaml:"any,omitempty" json:"any,omitempty"`
	Not *Condition  `yaml:"not,omitempty" json:"not,omitempty"`

	Field string `yaml:"field,omitempty" json:"field,omitempty"`
	Op    string `yaml:"op,omitempty" json:"op,omitempty"`
	Value any    `yaml:"value,omitempty" json:"value,omitempty"`
}

// Normalize turns integral float64 values (how JSON decodes every number)
// back into integers, so a rule round-tripped through the database renders
// as 10000000 rather than 1e+07.
func (c *Condition) Normalize() {
	if c == nil {
		return
	}
	c.Value = normalizeValue(c.Value)
	for i := range c.All {
		c.All[i].Normalize()
	}
	for i := range c.Any {
		c.Any[i].Normalize()
	}
	if c.Not != nil {
		c.Not.Normalize()
	}
}

func normalizeValue(v any) any {
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) && x < 1e15 && x > -1e15 {
			return int64(x)
		}
	case []any:
		for i := range x {
			x[i] = normalizeValue(x[i])
		}
	}
	return v
}

// Route sends alerts to a target on channels.
//
//	to:       team:<id> (every member) | oncall:<id> (who is on call now) | user:<id>
//	channels: email, sms, phone, ui, or "preferences" to use each recipient's
//	          own severity/working-hours preferences
type Route struct {
	To       string   `yaml:"to" json:"to"`
	Channels []string `yaml:"channels" json:"channels"`
}

// Presentation controls how an alert looks. Title and Body are templates.
type Presentation struct {
	Title    string   `yaml:"title,omitempty" json:"title,omitempty"`
	Body     string   `yaml:"body,omitempty" json:"body,omitempty"`
	Fields   []string `yaml:"fields,omitempty" json:"fields,omitempty"`
	Severity string   `yaml:"severity,omitempty" json:"severity,omitempty"` // fixed level or template, default {{$severity}}
	Color    string   `yaml:"color,omitempty" json:"color,omitempty"`       // red|orange|amber|blue|green|gray, default by severity
}

// Dedup folds repeated matches into one open alert with a counter.
type Dedup struct {
	Key    string `yaml:"key,omitempty" json:"key,omitempty"`       // template; default derived from type/service/error code
	Window string `yaml:"window,omitempty" json:"window,omitempty"` // reserved (use group.window / autoResolve.after)
}

// Group makes one alert per group key per time window ("one alert per
// instrument every 5 minutes"). Notifications for the window go out as a
// single digest when it closes, and the window's alert closes with it.
type Group struct {
	By     []string `yaml:"by" json:"by"`         // field paths or facets, e.g. [instrument]
	Window string   `yaml:"window" json:"window"` // e.g. 5m
}

// AutoResolve closes alerts without a human:
//
//	when:  an event matching this condition (e.g. status = resolved) resolves
//	       the open alert with the same fingerprint, and pages "all clear";
//	after: an alert with no new matching event for this long resolves itself.
type AutoResolve struct {
	When  *Condition `yaml:"when,omitempty" json:"when,omitempty"`
	After string     `yaml:"after,omitempty" json:"after,omitempty"`
}
