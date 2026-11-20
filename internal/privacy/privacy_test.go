package privacy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/undeluro/kardan-alerts/internal/schema"
)

func str(s string) gjson.Result { return gjson.Result{Type: gjson.String, Str: s} }

func TestLevels(t *testing.T) {
	p := Default()
	if l := p.LevelOf("debtorIban", schema.KindIBAN, str("DE89370400440532013000")); l != Confidential {
		t.Errorf("iban kind: %s", l)
	}
	if l := p.LevelOf("whatever", "", str("GB82 WEST 1234 5698 7654 32")); l != Confidential {
		t.Errorf("IBAN-looking value must be confidential anywhere: %s", l)
	}
	if l := p.LevelOf("payload.desk", schema.KindDesk, str("rates")); l != Public {
		t.Errorf("desk: %s", l)
	}
	if l := p.LevelOf("$account", "", str("x")); l != Confidential {
		t.Errorf("$account facet: %s", l)
	}
	if p.ChannelLevel("sms") != Public || p.ChannelLevel("email") != Internal || p.ChannelLevel("ui") != Confidential {
		t.Error("default channel levels")
	}
}

func TestLoadOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "classification.yaml")
	_ = os.WriteFile(path, []byte(`
kinds: { amount: internal }
fields:
  - { path: "client.**", level: confidential }
  - { path: "payload.notional", level: public }
channels: { email: public }
`), 0o644)
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.LevelOf("client.address.city", "", str("Leeds")) != Confidential {
		t.Error("** glob should cover nested fields")
	}
	if p.LevelOf("payload.notional", schema.KindAmount, gjson.Result{Type: gjson.Number, Num: 1}) != Public {
		t.Error("explicit field rule should win over the kind default")
	}
	if p.LevelOf("trade.amount", schema.KindAmount, gjson.Result{Type: gjson.Number, Num: 1}) != Internal {
		t.Error("kind override")
	}
	if p.ChannelLevel("email") != Public {
		t.Error("channel override")
	}
	_ = os.WriteFile(path, []byte(`channels: { sms: secret }`), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("unknown level should fail")
	}
}

func TestMask(t *testing.T) {
	cases := map[string]string{
		"DE89370400440532013000": "****3000",
		"Acme Industrial GmbH":   "A. I. G.",
		"abc":                    "****",
		"":                       "",
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}
