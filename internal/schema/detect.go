package schema

import (
	"math/big"
	"regexp"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

// Finance-aware field detection. When an unseen schema arrives, every leaf
// field is checked: identifiers are validated with their real check digits
// (ISIN Luhn, LEI / IBAN mod 97), so "US0378331005" is recognised as an
// instrument and "GB82WEST12345698765432" as an account that must be masked
// on SMS, without anyone describing the schema first.

const (
	KindISIN           = "isin"
	KindLEI            = "lei"
	KindIBAN           = "iban"
	KindBIC            = "bic"
	KindCurrency       = "currency"
	KindAmount         = "amount"
	KindQuantity       = "quantity"
	KindPrice          = "price"
	KindReference      = "reference" // trade / order / payment ids
	KindCounterparty   = "counterparty"
	KindDesk           = "desk"
	KindInstrument     = "instrument"
	KindSettlementDate = "settlementDate"
	KindAccount        = "account"
	KindPartyName      = "partyName"
	KindContact        = "contact"
)

var (
	isinRe = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{9}[0-9]$`)
	leiRe  = regexp.MustCompile(`^[A-Z0-9]{18}[0-9]{2}$`)
	ibanRe = regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}$`)
	bicRe  = regexp.MustCompile(`^[A-Z]{4}[A-Z]{2}[A-Z0-9]{2}([A-Z0-9]{3})?$`)
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

	amountKey  = regexp.MustCompile(`(?i)(notional|amount|amt|nominal|principal|exposure|value|mktval|pnl)`)
	qtyKey     = regexp.MustCompile(`(?i)(qty|quantity|size|units)`)
	priceKey   = regexp.MustCompile(`(?i)(price|px|rate)$`)
	refKey     = regexp.MustCompile(`(?i)^(trade|order|clord|exec|deal|payment|txn|transaction|workflow)_?id$|^(uetr|endtoendid|orgnlendtoendid|e2eid|msgid|clordid|orderid|execid|tradeid)$`)
	cptyKey    = regexp.MustCompile(`(?i)(counterparty|cpty|counter_party)`)
	deskKey    = regexp.MustCompile(`(?i)^(desk|book|portfolio|tradingbook)$`)
	instrKey   = regexp.MustCompile(`(?i)^(instrument|symbol|ticker|security|securityid|cusip|ric)$`)
	ccyKey     = regexp.MustCompile(`(?i)^(@?ccy|currency|cur|curr|settlementcurrency|currencycode)$`)
	bicKey     = regexp.MustCompile(`(?i)(bic|bicfi|swift)`)
	settlKey   = regexp.MustCompile(`(?i)(settl.*(dt|date)|value_?date|intrbksttlmdt)`)
	acctKey    = regexp.MustCompile(`(?i)(account|acct)(no|number|id)?$`)
	nameKey    = regexp.MustCompile(`(?i)^(nm|name|fullname)$`)
	partyKey   = regexp.MustCompile(`(?i)^(debtor|creditor|beneficiary|payer|payee|client|customer|ordering_?customer)(_?name)?$`)
	partyPath  = regexp.MustCompile(`(?i)(dbtr|cdtr|debtor|creditor|client|customer|beneficiary|payer|payee|ultmt)`)
	contactKey = regexp.MustCompile(`(?i)(email|phone|mobile|msisdn)`)
)

// ISO 4217 codes most likely to appear in capital-markets traffic.
var currencies = map[string]bool{}

func init() {
	for _, c := range strings.Fields(`USD EUR GBP JPY CHF CAD AUD NZD SEK NOK DKK PLN CZK HUF RON BGN HKD SGD CNY CNH
		KRW INR IDR MYR THB TWD PHP ZAR TRY ILS AED SAR QAR KWD BRL MXN CLP COP PEN ARS RUB UAH XAU XAG`) {
		currencies[c] = true
	}
}

// Detect classifies one field. It returns "" when nothing finance-specific is found.
func Detect(path string, r gjson.Result) (kind string, sensitive bool) {
	key := lastKey(path)
	switch r.Type {
	case gjson.String:
		v := strings.TrimSpace(r.Str)
		switch {
		case IsIBAN(v):
			return KindIBAN, true
		case IsISIN(v):
			return KindISIN, false
		case IsLEI(v):
			return KindLEI, false
		case bicKey.MatchString(key) && bicRe.MatchString(v):
			return KindBIC, false
		case ccyKey.MatchString(key) && currencies[v]:
			return KindCurrency, false
		}
	case gjson.Number:
		switch {
		case qtyKey.MatchString(key):
			return KindQuantity, false
		case priceKey.MatchString(key):
			return KindPrice, false
		case amountKey.MatchString(key):
			return KindAmount, false
		}
	}
	switch {
	case refKey.MatchString(key):
		return KindReference, false
	case cptyKey.MatchString(key):
		return KindCounterparty, false
	case deskKey.MatchString(key):
		return KindDesk, false
	case instrKey.MatchString(key):
		return KindInstrument, false
	case settlKey.MatchString(key) && dateRe.MatchString(r.String()):
		return KindSettlementDate, false
	case acctKey.MatchString(key):
		return KindAccount, true
	case nameKey.MatchString(key) && partyPath.MatchString(path):
		return KindPartyName, true
	case partyKey.MatchString(key) && r.Type == gjson.String:
		return KindPartyName, true
	case contactKey.MatchString(key):
		return KindContact, true
	}
	return "", false
}

func lastKey(path string) string {
	parts := strings.Split(path, ".")
	for i := len(parts) - 1; i >= 0; i-- {
		if p := parts[i]; p != "" && !isAllDigits(p) {
			return p
		}
	}
	return path
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// IsISIN validates the format and the Luhn check digit.
func IsISIN(s string) bool {
	if !isinRe.MatchString(s) {
		return false
	}
	var digits strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			digits.WriteString(itoa(int(r-'A') + 10))
		} else {
			digits.WriteRune(r)
		}
	}
	return luhn(digits.String())
}

func luhn(d string) bool {
	sum, double := 0, false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}

// IsLEI validates ISO 17442 (ISO 7064 mod 97-10).
func IsLEI(s string) bool { return leiRe.MatchString(s) && mod97(s) == 1 }

// IsIBAN validates ISO 13616: rearranged, letters expanded, mod 97 == 1.
func IsIBAN(s string) bool {
	s = strings.ReplaceAll(s, " ", "")
	return ibanRe.MatchString(s) && mod97(s[4:]+s[:4]) == 1
}

func mod97(s string) int {
	var b strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			b.WriteString(itoa(int(r-'A') + 10))
		} else {
			b.WriteRune(r)
		}
	}
	n, ok := new(big.Int).SetString(b.String(), 10)
	if !ok {
		return -1
	}
	return int(new(big.Int).Mod(n, big.NewInt(97)).Int64())
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// AutoFacets maps detected fields onto canonical finance facets for a schema.
// Shallower fields win (a top-level summary beats a deeply nested original),
// and more specific keys (notional over amount) are preferred.
func AutoFacets(fields []FieldInfo) map[string]string {
	fields = append([]FieldInfo(nil), fields...)
	sort.SliceStable(fields, func(i, j int) bool {
		di, dj := strings.Count(fields[i].Path, "."), strings.Count(fields[j].Path, ".")
		if di != dj {
			return di < dj
		}
		return fields[i].Path < fields[j].Path
	})
	out := map[string]string{}
	set := func(facet, path string) {
		if _, ok := out[facet]; !ok {
			out[facet] = path
		}
	}
	// Preferred matches first.
	for _, f := range fields {
		k := strings.ToLower(lastKey(f.Path))
		switch {
		case f.Detected == KindAmount && strings.Contains(k, "notional"):
			set("$notional", f.Path)
		case f.Detected == KindISIN:
			set("$instrument", f.Path)
		case f.Detected == KindCounterparty:
			set("$counterparty", f.Path)
		}
	}
	for _, f := range fields {
		switch f.Detected {
		case KindAmount:
			set("$notional", f.Path)
		case KindInstrument:
			set("$instrument", f.Path)
		case KindLEI:
			set("$counterparty", f.Path)
		case KindCurrency:
			set("$currency", f.Path)
		case KindReference:
			set("$tradeId", f.Path)
		case KindDesk:
			set("$desk", f.Path)
		case KindIBAN, KindAccount:
			set("$account", f.Path)
		}
	}
	return out
}
