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
	Runbooks     []string     `yaml:"runbooks,omitempty" json:"runbooks,omitempty"`

	// Origin records where the rule came from ("file:<name>" or "api").
	Origin string `yaml:"-" json:"origin,omitempty"`
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

// Route sends alerts to a target ("team:<id>" or "user:<id>") on channels
// (email, sms, phone, ui).
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

// Dedup folds repeated matches into one alert with a counter.
type Dedup struct {
	Key    string `yaml:"key,omitempty" json:"key,omitempty"`       // template; default derived from type/service/error code
	Window string `yaml:"window,omitempty" json:"window,omitempty"` // Go duration, default from DEDUP_WINDOW
}
