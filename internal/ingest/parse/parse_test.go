package parse

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

const fixReject = `2026-11-20T09:31:02.114Z IN 8=FIX.4.4|9=210|35=8|49=EXCH|56=GSDESK|34=1182|52=20261120-09:31:02.110|` +
	`37=O-77121|11=C-9921|17=E-1|150=8|39=8|55=EURUSD|54=1|38=25000000|44=1.0842|15=EUR|103=3|` +
	`58=Credit limit exceeded for counterparty|448=ACME|448=BETA|10=128|`

const pacs002 = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.002.001.10">
  <FIToFIPmtStsRpt>
    <GrpHdr><MsgId>STS-20261120-0001</MsgId><CreDtTm>2026-11-20T09:40:00Z</CreDtTm></GrpHdr>
    <TxInfAndSts>
      <OrgnlEndToEndId>E2E-INV-55821</OrgnlEndToEndId>
      <OrgnlUETR>4f8a2c1e-9b7d-4e2a-8c3f-1a2b3c4d5e6f</OrgnlUETR>
      <TxSts>RJCT</TxSts>
      <StsRsnInf><Rsn><Cd>AC04</Cd></Rsn><AddtlInf>Account closed on 2026-11-01</AddtlInf></StsRsnInf>
      <OrgnlTxRef>
        <IntrBkSttlmAmt Ccy="EUR">250000.00</IntrBkSttlmAmt>
        <Dbtr><Nm>Acme Industrial GmbH</Nm></Dbtr>
        <DbtrAcct><Id><IBAN>DE89370400440532013000</IBAN></Id></DbtrAcct>
        <Cdtr><Nm>Bolt Logistics Ltd</Nm></Cdtr>
        <CdtrAcct><Id><IBAN>GB82WEST12345698765432</IBAN></Id></CdtrAcct>
      </OrgnlTxRef>
    </TxInfAndSts>
  </FIToFIPmtStsRpt>
</Document>`

func get(t *testing.T, r Result, path string) gjson.Result {
	t.Helper()
	return gjson.GetBytes(r.Payload, path)
}

func TestFIX(t *testing.T) {
	r, err := Auto{}.Parse([]byte(fixReject))
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "fix" {
		t.Fatalf("format = %s", r.Format)
	}
	checks := map[string]string{
		"msgType": "ExecutionReport", "type": "fix.ExecutionReport", "OrdStatusName": "Rejected",
		"SideName": "Buy", "Symbol": "EURUSD", "Currency": "EUR", "OrdRejReasonName": "OrderExceedsLimit",
		"severity": "error", "notional": "27105000", "OrderQty": "25000000", "PartyID.1": "BETA", "tags.35": "8",
	}
	for path, want := range checks {
		if got := get(t, r, path).String(); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if !strings.HasPrefix(r.Raw, "8=FIX.4.4|") {
		t.Errorf("raw should start at the FIX message, got %q", r.Raw[:20])
	}

	soh := strings.ReplaceAll("8=FIX.4.4|35=0|49=A|56=B|34=1|10=000|", "|", "\x01")
	r, err = FIX{}.Parse([]byte(soh))
	if err != nil || get(t, r, "msgType").String() != "Heartbeat" || get(t, r, "severity").String() != "info" {
		t.Errorf("SOH heartbeat: %v %s", err, r.Payload)
	}
}

func TestISO20022(t *testing.T) {
	r, err := Auto{}.Parse([]byte(pacs002))
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "iso20022" {
		t.Fatalf("format = %s", r.Format)
	}
	checks := map[string]string{
		"type": "pacs.002", "messageType": "pacs.002.001.10", "status": "RJCT", "reasonCode": "AC04",
		"reasonName": "ClosedAccountNumber", "amount": "250000", "currency": "EUR", "severity": "error",
		"endToEndId": "E2E-INV-55821", "debtor": "Acme Industrial GmbH", "creditorIban": "GB82WEST12345698765432",
		"Document.FIToFIPmtStsRpt.TxInfAndSts.OrgnlTxRef.IntrBkSttlmAmt.@Ccy": "EUR",
	}
	for path, want := range checks {
		if got := get(t, r, path).String(); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestGenericXML(t *testing.T) {
	r, err := Auto{}.Parse([]byte(`<alert level="high"><name>disk</name><name>cpu</name><value>91.5</value></alert>`))
	if err != nil || r.Format != "xml" {
		t.Fatalf("%v %s", err, r.Format)
	}
	if get(t, r, "alert.@level").String() != "high" || get(t, r, "alert.name.1").String() != "cpu" ||
		get(t, r, "alert.value").Float() != 91.5 || get(t, r, "type").String() != "xml.alert" {
		t.Errorf("payload = %s", r.Payload)
	}
}

func TestLogfmt(t *testing.T) {
	line := `2026-11-20T09:15:01Z WARN level=warn app=pricing-engine-3 instrument=EURUSD latencyMs=1240 msg="quote latency above threshold"`
	r, err := Auto{}.Parse([]byte(line))
	if err != nil || r.Format != "logfmt" {
		t.Fatalf("%v %s", err, r.Format)
	}
	if get(t, r, "app").String() != "pricing-engine-3" || get(t, r, "latencyMs").Int() != 1240 ||
		get(t, r, "msg").String() != "quote latency above threshold" || get(t, r, "level").String() != "warn" {
		t.Errorf("payload = %s", r.Payload)
	}
	r, _ = Logfmt{}.Parse([]byte(`ERROR svc=a code=7`))
	if get(t, r, "level").String() != "ERROR" {
		t.Errorf("level from prefix: %s", r.Payload)
	}
}

func TestRegexAndText(t *testing.T) {
	p, err := New(Spec{Name: "regex", Pattern: `^(?P<ts>\S+) (?P<level>\w+) \[(?P<app>[^\]]+)\] (?P<message>.*)$`})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := p.Parse([]byte(`2026-11-20T09:15:01Z ERROR [pricing-engine-1] cache miss storm`))
	if get(t, r, "app").String() != "pricing-engine-1" || get(t, r, "level").String() != "ERROR" {
		t.Errorf("regex payload = %s", r.Payload)
	}
	r, _ = p.Parse([]byte("garbage that does not match"))
	if r.Format != "regex-unmatched" || get(t, r, "message").String() == "" {
		t.Errorf("unmatched lines must be kept: %s", r.Payload)
	}
	if _, err := New(Spec{Name: "regex", Pattern: `\d+`}); err == nil {
		t.Error("pattern without named groups should be rejected")
	}

	r, _ = Auto{}.Parse([]byte("2026-11-20 10:00:01 WARNING disk almost full on node-7"))
	if r.Format != "text" || get(t, r, "level").String() != "WARNING" {
		t.Errorf("text = %s %s", r.Format, r.Payload)
	}
}

func TestAutoJSON(t *testing.T) {
	r, err := Auto{}.Parse([]byte(`  {"a":1} `))
	if err != nil || r.Format != "json" || string(r.Payload) != `{"a":1}` || r.Raw != "" {
		t.Errorf("%v %s %s", err, r.Format, r.Payload)
	}
}
