package runbooks

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/undeluro/kardan-alerts/internal/rules"
)

// Meta is what a runbook needs besides its steps. It comes from YAML keys,
// from front matter at the top of a Markdown/text file, or from the API.
type Meta struct {
	ID      string           `yaml:"runbook,omitempty" json:"id,omitempty"`
	Name    string           `yaml:"name,omitempty" json:"name,omitempty"`
	Team    string           `yaml:"team,omitempty" json:"team,omitempty"`
	Format  string           `yaml:"format,omitempty" json:"format,omitempty"`
	Matches *rules.Condition `yaml:"-" json:"matches,omitempty"`
	Rules   []string         `yaml:"rules,omitempty" json:"rules,omitempty"`
	Schemas []string         `yaml:"schemas,omitempty" json:"schemas,omitempty"`
	Auto    bool             `yaml:"auto,omitempty" json:"auto,omitempty"`
}

// Parse reads a runbook in any supported format. meta fills in what the text
// does not say (front matter and YAML keys win over meta).
func Parse(source string, meta Meta) (*Runbook, error) {
	body, front, err := splitFrontMatter(source)
	if err != nil {
		return nil, err
	}
	m := meta
	if front != nil {
		fm, err := metaFromMap(front)
		if err != nil {
			return nil, fmt.Errorf("front matter: %w", err)
		}
		m = merge(fm, meta)
	}
	format := m.Format
	if format == "" {
		format = Detect(body)
	}
	var rb *Runbook
	switch format {
	case FormatYAML:
		rb, err = parseYAML(body)
	case FormatMarkdown, FormatChecklist:
		rb = parseMarkdown(body, format == FormatChecklist)
	case FormatLink:
		rb, err = parseLink(body)
	case FormatText:
		rb = parseText(body)
	default:
		return nil, fmt.Errorf("unknown format %q (yaml, markdown, checklist, link, text)", format)
	}
	if err != nil {
		return nil, err
	}
	if format == FormatMarkdown && looksLikeChecklist(body) {
		format = FormatChecklist
	}
	rb.Format, rb.Source = format, source
	// The text's own metadata wins over what the caller passed.
	if rb.ID == "" {
		rb.ID = m.ID
	}
	if rb.Name == "" {
		rb.Name = m.Name
	}
	if rb.Team == "" {
		rb.Team = m.Team
	}
	if rb.Matches == nil {
		rb.Matches = m.Matches
	}
	if len(rb.Rules) == 0 {
		rb.Rules = m.Rules
	}
	if len(rb.Schemas) == 0 {
		rb.Schemas = m.Schemas
	}
	rb.Auto = rb.Auto || m.Auto
	if rb.ID == "" && rb.Name != "" {
		rb.ID = Slug(rb.Name)
	}
	if len(rb.Steps) == 0 && rb.URL == "" {
		return nil, fmt.Errorf("no steps found: write a list, sentences with what to do, YAML steps or a link")
	}
	if err := rb.compile(); err != nil {
		return nil, err
	}
	return rb, nil
}

// Detect guesses the format of a runbook body.
func Detect(body string) string {
	t := strings.TrimSpace(body)
	if t == "" {
		return FormatText
	}
	if urlOnly(t) {
		return FormatLink
	}
	var probe map[string]any
	if yaml.Unmarshal([]byte(t), &probe) == nil && probe != nil {
		if _, ok := probe["steps"]; ok {
			return FormatYAML
		}
	}
	lines := strings.Split(t, "\n")
	items, headings := 0, 0
	for _, l := range lines {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "#") && !strings.HasPrefix(l, "#!") && len(l) > 1 && (l[1] == ' ' || l[1] == '#'):
			headings++
		case listItem.MatchString(l):
			items++
		}
	}
	if looksLikeChecklist(t) {
		return FormatChecklist
	}
	if items >= 2 || (headings > 0 && items > 0) {
		return FormatMarkdown
	}
	return FormatText
}

var (
	listItem    = regexp.MustCompile(`^(?:[-*+•]|\d{1,3}[.)])\s+`)
	checkboxRe  = regexp.MustCompile(`^(?:[-*+•]\s+)?(?:\[[ xX]\]|☐|☑|✅)\s+`)
	urlRe       = regexp.MustCompile(`https?://[^\s)>\]]+`)
	frontMatter = regexp.MustCompile(`(?s)^\s*---\r?\n(.*?)\r?\n---\s*(?:\r?\n|$)`)
)

func looksLikeChecklist(body string) bool {
	boxes, items := 0, 0
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSpace(l)
		if checkboxRe.MatchString(l) {
			boxes++
			items++
		} else if listItem.MatchString(l) {
			items++
		}
	}
	return boxes >= 2 && boxes*2 >= items
}

func urlOnly(t string) bool {
	lines := nonEmpty(strings.Split(t, "\n"))
	if len(lines) == 0 || len(lines) > 3 {
		return false
	}
	urls := 0
	for _, l := range lines {
		if urlRe.MatchString(l) {
			urls++
		}
	}
	// "Settlement fails: https://wiki/..." or just the URL.
	return urls == 1 && len(strings.Fields(strings.Join(lines, " "))) <= 12
}

func splitFrontMatter(src string) (string, map[string]any, error) {
	m := frontMatter.FindStringSubmatchIndex(src)
	if m == nil {
		return src, nil, nil
	}
	var front map[string]any
	if err := yaml.Unmarshal([]byte(src[m[2]:m[3]]), &front); err != nil {
		return "", nil, fmt.Errorf("front matter: %w", err)
	}
	return src[m[1]:], front, nil
}

// ---- YAML (Appendix B) ----------------------------------------------------------

var stepKeys = map[string]bool{"check": true, "action": true, "text": true, "step": true, "do": true, "note": true,
	"if": true, "approval": true, "then": true, "every": true, "label": true, "title": true, "name": true}

func parseYAML(body string) (*Runbook, error) {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	meta, err := metaFromMap(raw)
	if err != nil {
		return nil, err
	}
	rb := &Runbook{ID: meta.ID, Name: meta.Name, Team: meta.Team, Matches: meta.Matches, Rules: meta.Rules,
		Schemas: meta.Schemas, Auto: meta.Auto}
	if v, ok := raw["url"].(string); ok {
		rb.URL = v
	}
	for _, k := range []string{"summary", "description"} {
		if v, ok := raw[k].(string); ok {
			rb.Summary = strings.TrimSpace(v)
		}
	}
	list, _ := raw["steps"].([]any)
	if list == nil {
		return nil, fmt.Errorf("yaml runbook needs a steps list")
	}
	for i, item := range list {
		switch v := item.(type) {
		case string: // a bare sentence: infer like free text
			s := Infer(v)
			rb.Steps = append(rb.Steps, s)
		case map[string]any:
			s, err := yamlStep(v)
			if err != nil {
				return nil, fmt.Errorf("steps[%d]: %w", i, err)
			}
			rb.Steps = append(rb.Steps, s)
		default:
			return nil, fmt.Errorf("steps[%d]: expected a map or a sentence", i)
		}
	}
	return rb, nil
}

func yamlStep(m map[string]any) (Step, error) {
	s := Step{Params: map[string]string{}}
	str := func(k string) string {
		if v, ok := m[k]; ok && v != nil {
			return scalar(v)
		}
		return ""
	}
	s.Action = str("action")
	if c := str("check"); c != "" {
		s.Kind, s.Text = KindCheck, c
	}
	for _, k := range []string{"text", "step", "do", "note", "label", "title", "name"} {
		if v := str(k); v != "" && s.Text == "" {
			s.Text = v
		}
	}
	s.If, s.Then, s.Every = str("if"), strings.ToLower(str("then")), str("every")
	s.Approval = strings.ToLower(str("approval"))
	if s.Approval == "true" || s.Approval == "yes" {
		s.Approval = "required"
	}
	if s.Approval == "false" || s.Approval == "no" {
		s.Approval = "none"
	}
	for k, v := range m {
		if !stepKeys[k] {
			s.Params[k] = scalar(v)
		}
	}
	if len(s.Params) == 0 {
		s.Params = nil
	}
	if s.Kind == "" {
		switch {
		case s.Action != "":
			s.Kind = KindAction
		case s.Text != "":
			// A prose step inside YAML: infer the action, keep explicit fields.
			inf := Infer(s.Text)
			s.Kind, s.Action, s.Inferred = inf.Kind, inf.Action, inf.Inferred
			if s.Params == nil {
				s.Params = inf.Params
			}
			if s.Approval == "" {
				s.Approval = inf.Approval
			}
		default:
			return s, fmt.Errorf("a step needs text, check or action")
		}
	}
	return s, nil
}

func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, p := range x {
			parts[i] = scalar(p)
		}
		return strings.Join(parts, ",")
	case map[string]any:
		b, _ := json.Marshal(x)
		return string(b)
	default:
		return fmt.Sprint(x)
	}
}

// metaFromMap reads runbook metadata keys from YAML or front matter.
func metaFromMap(raw map[string]any) (Meta, error) {
	var m Meta
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := raw[k]; ok && v != nil {
				return strings.TrimSpace(scalar(v))
			}
		}
		return ""
	}
	m.ID = get("runbook", "id")
	m.Name = get("name", "title")
	m.Team = get("team")
	m.Format = get("format")
	m.Rules = list(raw["rules"])
	if r := get("rule"); r != "" {
		m.Rules = append(m.Rules, r)
	}
	m.Schemas = list(raw["schemas"])
	if v, ok := raw["auto"].(bool); ok {
		m.Auto = v
	}
	if v, ok := raw["matches"]; ok && v != nil {
		c, err := MatchesCondition(v)
		if err != nil {
			return m, fmt.Errorf("matches: %w", err)
		}
		m.Matches = c
	}
	return m, nil
}

func list(v any) []string {
	switch x := v.(type) {
	case []any:
		var out []string
		for _, p := range x {
			out = append(out, scalar(p))
		}
		return out
	case string:
		var out []string
		for _, p := range strings.Split(x, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

// MatchesCondition accepts Appendix B's simple form, a map of field to
// expected value ({type: WORKFLOW_FAILED, "error.code": TIMEOUT}; lists mean
// "any of", values with * are globs), or a full rule condition (all/any/not).
func MatchesCondition(v any) (*rules.Condition, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a map of field: value, or a condition with all/any")
	}
	for _, k := range []string{"all", "any", "not", "field"} {
		if _, ok := m[k]; ok {
			b, _ := json.Marshal(m)
			var c rules.Condition
			if err := json.Unmarshal(b, &c); err != nil {
				return nil, err
			}
			return &c, nil
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	c := &rules.Condition{}
	for _, k := range keys {
		leaf := rules.Condition{Field: k, Op: "eq", Value: m[k]}
		switch x := m[k].(type) {
		case []any:
			leaf.Op = "in"
		case string:
			if strings.ContainsAny(x, "*?") {
				leaf.Op = "glob"
			}
		}
		c.All = append(c.All, leaf)
	}
	return c, nil
}

func merge(primary, fallback Meta) Meta {
	if primary.ID == "" {
		primary.ID = fallback.ID
	}
	if primary.Name == "" {
		primary.Name = fallback.Name
	}
	if primary.Team == "" {
		primary.Team = fallback.Team
	}
	if primary.Format == "" {
		primary.Format = fallback.Format
	}
	if primary.Matches == nil {
		primary.Matches = fallback.Matches
	}
	if len(primary.Rules) == 0 {
		primary.Rules = fallback.Rules
	}
	if len(primary.Schemas) == 0 {
		primary.Schemas = fallback.Schemas
	}
	primary.Auto = primary.Auto || fallback.Auto
	return primary
}

// ---- Markdown and checklists ----------------------------------------------------

var (
	headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mdNoise   = strings.NewReplacer("**", "", "__", "", "`", "")
	mdLink    = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

func parseMarkdown(body string, checklist bool) *Runbook {
	rb := &Runbook{}
	var summary []string
	var current *Step
	flush := func() {
		if current != nil {
			rb.Steps = append(rb.Steps, finishProse(*current)...)
			current = nil
		}
	}
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		t := strings.TrimSpace(line)
		if t == "" {
			flush()
			continue
		}
		if h := headingRe.FindStringSubmatch(t); h != nil {
			flush()
			if rb.Name == "" {
				rb.Name = clean(h[2])
			}
			continue
		}
		boxed := checkboxRe.MatchString(t)
		if boxed || listItem.MatchString(t) {
			flush()
			text := t
			if boxed {
				text = checkboxRe.ReplaceAllString(text, "")
			} else {
				text = listItem.ReplaceAllString(text, "")
			}
			s := Step{Text: clean(text)}
			if boxed || checklist {
				s.Kind = KindCheck
			}
			current = &s
			continue
		}
		if current != nil && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			current.Text += " " + clean(t) // continuation of a list item
			continue
		}
		flush()
		if len(rb.Steps) == 0 {
			summary = append(summary, clean(t))
		} else {
			rb.Steps = append(rb.Steps, finishProse(Step{Text: clean(t)})...)
		}
	}
	flush()
	rb.Summary = strings.Join(summary, " ")
	return rb
}

func clean(s string) string {
	s = mdLink.ReplaceAllString(s, "$1 ($2)")
	return strings.TrimSpace(mdNoise.Replace(s))
}

// finishProse infers the action of a written step, splitting "check X and
// roll it back" into two steps when the second part is something Kardan can do.
func finishProse(s Step) []Step {
	cond, rest := splitCondition(s.Text)
	parts := splitCompound(rest)
	out := make([]Step, 0, len(parts))
	for _, p := range parts {
		inf := Infer(p)
		if s.Kind == KindCheck && inf.Kind == KindText {
			inf.Kind = KindCheck
		}
		inf.Text = p
		if len(parts) == 1 {
			inf.Text = rest
		}
		if inf.If == "" {
			inf.If = cond
		}
		out = append(out, inf)
	}
	// Approval mentioned anywhere in the item applies to its actions.
	if approvalRe.MatchString(s.Text) {
		for i := range out {
			if out[i].Kind == KindAction {
				out[i].Approval = "required"
			}
		}
	}
	return out
}

// ---- links ---------------------------------------------------------------------

func parseLink(body string) (*Runbook, error) {
	t := strings.TrimSpace(body)
	u := urlRe.FindString(t)
	if u == "" {
		return nil, fmt.Errorf("no URL found")
	}
	title := strings.TrimSpace(strings.Trim(strings.Replace(t, u, "", 1), ":-–— \n"))
	rb := &Runbook{URL: u, Name: title}
	text := "Follow the runbook at " + u
	if title != "" {
		text = "Follow \"" + title + "\": " + u
	}
	rb.Steps = []Step{{Kind: KindText, Text: text}}
	rb.Notes = append(rb.Notes, "A link: the page is shown as a link and not fetched (no outbound requests to unknown hosts).")
	return rb, nil
}

// ---- free text -----------------------------------------------------------------

var sentenceEnd = regexp.MustCompile(`([.!?;])\s+|\n+`)

func parseText(body string) *Runbook {
	rb := &Runbook{}
	var context []string
	for _, sentence := range sentences(body) {
		steps := proseSteps(sentence)
		if len(steps) == 0 {
			context = append(context, sentence)
			continue
		}
		rb.Steps = append(rb.Steps, steps...)
	}
	rb.Summary = strings.Join(context, " ")
	if rb.Summary != "" {
		rb.Notes = append(rb.Notes, "Sentences without an instruction are kept as context.")
	}
	return rb
}

func sentences(body string) []string {
	idx := sentenceEnd.FindAllStringSubmatchIndex(body, -1)
	var out []string
	start := 0
	for _, m := range idx {
		end := m[1]
		if m[2] >= 0 {
			end = m[3] // keep the punctuation with the sentence
		} else {
			end = m[0]
		}
		if s := strings.TrimSpace(body[start:end]); s != "" {
			out = append(out, s)
		}
		start = m[1]
	}
	if s := strings.TrimSpace(body[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// proseSteps turns one sentence into steps: "Restart X, wait 2 minutes, and
// if latency is still high, call the pricing on-call person." gives restart,
// wait, and a conditional call. A sentence with no instruction gives none.
func proseSteps(sentence string) []Step {
	s := strings.TrimRight(sentence, ".!;")
	var out []Step
	pending := ""
	for _, clause := range splitClauses(s) {
		c := stripConnector(clause)
		if c == "" {
			continue
		}
		if cond, ok := conditionClause(c); ok {
			pending = cond
			continue
		}
		cond, rest := splitCondition(c)
		if cond != "" {
			pending, c = cond, rest
		}
		if !imperative(c) {
			continue
		}
		for _, part := range splitCompound(c) {
			st := Infer(part)
			st.Text = upperFirst(part)
			st.If = pending
			out = append(out, st)
		}
		pending = ""
	}
	return out
}

func splitClauses(s string) []string {
	return strings.Split(s, ",")
}

var connectors = []string{"and then ", "and ", "then ", "but ", "so ", "also ", "first ", "next ", "finally ",
	"afterwards ", "after that ", "otherwise "}

func stripConnector(c string) string {
	c = strings.TrimSpace(c)
	for changed := true; changed; {
		changed = false
		lc := strings.ToLower(c)
		for _, p := range connectors {
			if strings.HasPrefix(lc, p) {
				c, changed = strings.TrimSpace(c[len(p):]), true
				break
			}
		}
	}
	return c
}

var condPrefix = regexp.MustCompile(`(?i)^(if|when|whenever|unless|once|in case)\s+(.+)$`)

// conditionClause recognises a clause that is only a condition: "if latency is
// still high" (the action follows after the comma).
func conditionClause(c string) (string, bool) {
	m := condPrefix.FindStringSubmatch(c)
	if m == nil {
		return "", false
	}
	cond := m[2]
	if strings.ToLower(m[1]) == "unless" {
		cond = "not: " + cond
	}
	return cond, true
}

// splitCondition separates "If one region: fail over" or "if X then Y".
func splitCondition(text string) (cond, rest string) {
	m := condPrefix.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", text
	}
	body := m[2]
	for _, sep := range []string{": ", " then ", ", "} {
		if i := strings.Index(body, sep); i > 0 {
			return strings.TrimSpace(body[:i]), upperFirst(strings.TrimSpace(body[i+len(sep):]))
		}
	}
	return "", text
}

// splitCompound splits "check the last deployment and roll it back" when the
// second half is an action Kardan can take; manual pairs stay one step.
func splitCompound(text string) []string {
	parts := strings.Split(text, " and ")
	if len(parts) < 2 {
		return []string{text}
	}
	var out []string
	cur := parts[0]
	for _, p := range parts[1:] {
		if imperative(p) && Infer(p).Kind == KindAction {
			out = append(out, strings.TrimSpace(cur))
			cur = p
			continue
		}
		cur += " and " + p
	}
	out = append(out, strings.TrimSpace(cur))
	for i := range out {
		out[i] = upperFirst(out[i])
	}
	return out
}

func upperFirst(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func nonEmpty(xs []string) []string {
	var out []string
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			out = append(out, strings.TrimSpace(x))
		}
	}
	return out
}

// Slug makes an id from a title: "HighErrorRate on payments-api" -> "higherrorrate-on-payments-api".
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	return out
}
