package directory

import (
	"fmt"
	"time"

	"github.com/undeluro/kardan-alerts/internal/schema"
)

// Throttle limits messages per person per channel (teams.yaml "throttle").
//
//	throttle:
//	  exempt: [critical]
//	  channels:
//	    sms: { max: 5, per: 10m }
type Throttle struct {
	Exempt   []string         `yaml:"exempt,omitempty" json:"exempt,omitempty"`
	Channels map[string]Limit `yaml:"channels,omitempty" json:"channels,omitempty"`

	exempt map[schema.Level]bool
}

type Limit struct {
	Max int    `yaml:"max" json:"max"`
	Per string `yaml:"per" json:"per"`

	per time.Duration
}

func (l Limit) Duration() time.Duration { return l.per }

// IsExempt reports whether a severity bypasses throttling.
func (t Throttle) IsExempt(severity string) bool { return t.exempt[schema.ParseLevel(severity)] }

func (t *Throttle) compile() error {
	t.exempt = map[schema.Level]bool{}
	exempt := t.Exempt
	if exempt == nil {
		exempt = []string{"critical"}
	}
	for _, s := range exempt {
		lvl := schema.ParseLevel(s)
		if lvl == schema.LevelUnknown {
			return fmt.Errorf("throttle.exempt: unknown severity %q", s)
		}
		t.exempt[lvl] = true
	}
	for ch, l := range t.Channels {
		if !validChannel(ch) || ch == "ui" {
			return fmt.Errorf("throttle.channels: %q cannot be throttled (email, sms, phone)", ch)
		}
		d, err := time.ParseDuration(l.Per)
		if err != nil || d < time.Second {
			return fmt.Errorf("throttle.channels.%s.per %q must be a duration like 10m", ch, l.Per)
		}
		if l.Max < 1 {
			return fmt.Errorf("throttle.channels.%s.max must be at least 1", ch)
		}
		l.per = d
		t.Channels[ch] = l
	}
	return nil
}

// Throttling returns the compiled throttle settings.
func (d *Directory) Throttling() Throttle { return d.Throttle }
