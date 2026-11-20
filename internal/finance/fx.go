// Package finance holds money-aware logic: FX normalisation to USD, the
// impact score used to rank alerts, and incident correlation settings.
package finance

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/undeluro/kardan-alerts/internal/schema"
)

// FX converts amounts to USD so exposure is comparable across currencies:
// a EUR 25M rejection and a JPY 4bn rejection rank by the same yardstick.
type FX struct {
	USDPer    map[string]float64 `yaml:"usdPer" json:"usdPer"` // 1 unit of currency = X USD
	AsOf      string             `yaml:"asOf" json:"asOf"`
	Source    string             `yaml:"source" json:"source"`
	LoadedAt  time.Time          `yaml:"-" json:"loadedAt"`
	normalize map[string]float64
}

// LoadFX reads config/fx.yaml. A missing file yields USD-only conversion.
func LoadFX(path string) (*FX, error) {
	fx := &FX{USDPer: map[string]float64{"USD": 1}}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := yaml.Unmarshal(data, fx); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	fx.normalize = map[string]float64{"USD": 1}
	for ccy, rate := range fx.USDPer {
		if rate <= 0 || math.IsNaN(rate) {
			return nil, fmt.Errorf("fx rate for %s must be positive", ccy)
		}
		fx.normalize[strings.ToUpper(ccy)] = rate
	}
	fx.LoadedAt = time.Now()
	return fx, nil
}

// ToUSD implements schema.Converter.
func (f *FX) ToUSD(amount float64, ccy string) (float64, bool) {
	if f == nil {
		return 0, false
	}
	rate, ok := f.normalize[strings.ToUpper(strings.TrimSpace(ccy))]
	if !ok {
		return 0, false
	}
	return amount * rate, true
}

var _ schema.Converter = (*FX)(nil)

// Impact ranks alerts 0-100: severity sets the base, money at risk adds up to
// 40 points on a log scale ($1M ≈ +20, $25M ≈ +29, $1bn ≈ +39), and
// urgency (time to a settlement cut-off, milestone 6) adds the rest.
func Impact(severity string, notionalUSD float64, hasNotional bool) int {
	base := map[schema.Level]float64{
		schema.LevelCritical: 60, schema.LevelError: 45, schema.LevelWarning: 25, schema.LevelInfo: 10,
	}[schema.ParseLevel(severity)]
	if base == 0 {
		base = 10
	}
	money := 0.0
	if hasNotional && notionalUSD > 0 {
		money = math.Min(40, 6.5*math.Log10(1+notionalUSD/1000))
	}
	return int(math.Min(100, math.Round(base+money)))
}

// Correlation decides how alerts fold into incidents.
type Correlation struct {
	Window    string   `yaml:"window" json:"window"`
	Keys      []string `yaml:"keys" json:"keys"`           // first key present on an alert wins
	MinAlerts int      `yaml:"minAlerts" json:"minAlerts"` // shown as an incident from this many alerts

	window time.Duration
}

func (c *Correlation) WindowDuration() time.Duration { return c.window }

// LoadCorrelation reads config/correlation.yaml (defaults when missing).
func LoadCorrelation(path string) (*Correlation, error) {
	c := &Correlation{Window: "15m", Keys: []string{"$counterparty", "$instrument", "$service"}, MinAlerts: 2}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := yaml.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	d, err := time.ParseDuration(c.Window)
	if err != nil || d <= 0 {
		return nil, fmt.Errorf("correlation window %q must be a positive duration like 15m", c.Window)
	}
	c.window = d
	if c.MinAlerts < 1 {
		c.MinAlerts = 2
	}
	return c, nil
}

// Key returns the incident key for an event, e.g. "$counterparty=ACME", or "".
func (c *Correlation) Key(d *schema.Doc) (field, value string) {
	if c == nil {
		return "", ""
	}
	for _, k := range c.Keys {
		v := strings.TrimSpace(d.Get(k).String())
		if v != "" {
			return k, v
		}
	}
	return "", ""
}
