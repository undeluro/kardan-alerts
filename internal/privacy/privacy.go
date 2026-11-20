// Package privacy classifies event fields and masks them per channel.
//
// A trade alert is fine in the authenticated UI with the client's name and
// IBAN, but an SMS to a personal phone or an email that may be forwarded
// should not carry them. Each field gets a level (public, internal,
// confidential); each channel has the highest level it may show in clear.
// Levels come from explicit path rules in config/classification.yaml, then
// from the finance kind detected by the schema registry (iban, partyName...),
// then from value patterns (an IBAN-looking value is confidential anywhere).
package privacy

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/tidwall/gjson"

	"github.com/undeluro/kardan-alerts/internal/schema"
)

type Level int

const (
	Public Level = iota
	Internal
	Confidential
)

func (l Level) String() string {
	switch l {
	case Internal:
		return "internal"
	case Confidential:
		return "confidential"
	default:
		return "public"
	}
}

func parseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "public":
		return Public, nil
	case "internal":
		return Internal, nil
	case "confidential":
		return Confidential, nil
	}
	return Public, fmt.Errorf("unknown level %q (public, internal, confidential)", s)
}

type fieldRule struct {
	re    *regexp.Regexp
	level Level
	glob  string
}

// Policy decides levels and masking.
type Policy struct {
	kinds    map[string]Level
	fields   []fieldRule
	channels map[string]Level
}

type file struct {
	Kinds  map[string]string `yaml:"kinds"`
	Fields []struct {
		Path  string `yaml:"path"`
		Level string `yaml:"level"`
	} `yaml:"fields"`
	Channels map[string]string `yaml:"channels"`
}

// Default is used when no classification.yaml exists.
func Default() *Policy {
	return &Policy{
		kinds: map[string]Level{
			schema.KindIBAN: Confidential, schema.KindAccount: Confidential, schema.KindPartyName: Confidential,
			schema.KindContact: Confidential, schema.KindLEI: Internal, schema.KindCounterparty: Internal,
		},
		channels: map[string]Level{"ui": Confidential, "email": Internal, "sms": Public, "phone": Public},
	}
}

// Load reads classification.yaml, layered over the defaults.
func Load(path string) (*Policy, error) {
	p := Default()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for k, v := range f.Kinds {
		l, err := parseLevel(v)
		if err != nil {
			return nil, fmt.Errorf("kinds.%s: %w", k, err)
		}
		p.kinds[k] = l
	}
	for i, fr := range f.Fields {
		l, err := parseLevel(fr.Level)
		if err != nil {
			return nil, fmt.Errorf("fields[%d]: %w", i, err)
		}
		re, err := globRe(fr.Path)
		if err != nil {
			return nil, fmt.Errorf("fields[%d]: %w", i, err)
		}
		p.fields = append(p.fields, fieldRule{re: re, level: l, glob: fr.Path})
	}
	for ch, v := range f.Channels {
		l, err := parseLevel(v)
		if err != nil {
			return nil, fmt.Errorf("channels.%s: %w", ch, err)
		}
		p.channels[ch] = l
	}
	return p, nil
}

// globRe matches field paths: "*" within a segment, "**" across segments.
func globRe(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("(?i)^")
	for i := 0; i < len(glob); i++ {
		switch {
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case glob[i] == '*':
			b.WriteString(`[^.]*`)
		default:
			b.WriteString(regexp.QuoteMeta(string(glob[i])))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// ChannelLevel is the highest level a channel shows unmasked.
func (p *Policy) ChannelLevel(channel string) Level {
	if l, ok := p.channels[channel]; ok {
		return l
	}
	return Public
}

var facetKinds = map[string]string{
	"$account": schema.KindAccount, "$counterparty": schema.KindCounterparty,
}

// LevelOf classifies a field by path, detected kind and value.
func (p *Policy) LevelOf(path, kind string, value gjson.Result) Level {
	lvl := Public
	for _, f := range p.fields {
		if f.re.MatchString(path) {
			return f.level // explicit rules are authoritative, in either direction
		}
	}
	if kind == "" {
		kind = facetKinds[path]
	}
	if l, ok := p.kinds[kind]; ok && l > lvl {
		lvl = l
	}
	if value.Type == gjson.String {
		v := strings.TrimSpace(value.Str)
		if schema.IsIBAN(strings.ReplaceAll(v, " ", "")) || emailRe.MatchString(v) {
			lvl = max(lvl, Confidential)
		}
	}
	return lvl
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[a-zA-Z]{2,}$`)

// Mask hides a value but keeps enough to recognise it: the last 4 characters
// of identifiers, initials of names.
func Mask(v string) string {
	v = strings.TrimSpace(v)
	r := []rune(v)
	switch {
	case len(r) == 0:
		return ""
	case strings.Contains(v, " ") && !strings.ContainsAny(v, "0123456789"):
		parts := strings.Fields(v)
		for i, p := range parts {
			pr := []rune(p)
			parts[i] = string(pr[0]) + "."
		}
		return strings.Join(parts, " ")
	case len(r) <= 4:
		return "****"
	default:
		return "****" + string(r[len(r)-4:])
	}
}
