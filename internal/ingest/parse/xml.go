package parse

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// XML converts any XML document to JSON (element local names as keys,
// attributes as "@name", text as "#text" when mixed with attributes, repeated
// elements as arrays). For ISO 20022 messages (pacs, camt, pain...) it also
// lifts a summary to the top level so rules stay short:
//
//	{"type":"pacs.002","messageType":"pacs.002.001.10","status":"RJCT","reasonCode":"AC04",
//	 "reasonName":"ClosedAccountNumber","amount":25000,"currency":"EUR","severity":"error",
//	 "Document":{...full tree...}}
type XML struct{}

type xnode struct {
	name     string
	attrs    map[string]string
	children []*xnode
	text     strings.Builder
}

func (XML) Parse(raw []byte) (Result, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = false
	var stack []*xnode
	var root *xnode
	var ns string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{}, fmt.Errorf("xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &xnode{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					if root == nil && ns == "" && a.Name.Local == "xmlns" {
						ns = a.Value
					}
					continue
				}
				n.attrs[a.Name.Local] = a.Value
			}
			if root == nil && t.Name.Space != "" && ns == "" {
				ns = t.Name.Space
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(t)
			}
		}
	}
	if root == nil {
		return Result{}, fmt.Errorf("xml: no root element")
	}

	out := map[string]any{root.name: toValue(root)}
	format := "xml"
	if mt := isoMessageType(ns); mt != "" {
		format = "iso20022"
		summarizeISO(out, root, mt)
	} else {
		out["type"] = "xml." + root.name
	}
	b, err := json.Marshal(out)
	return Result{Payload: b, Format: format, Raw: string(bytes.TrimSpace(raw))}, err
}

func toValue(n *xnode) any {
	text := strings.TrimSpace(n.text.String())
	if len(n.children) == 0 && len(n.attrs) == 0 {
		return typedXML(text)
	}
	m := map[string]any{}
	for k, v := range n.attrs {
		m["@"+k] = v
	}
	if text != "" {
		m["#text"] = typedXML(text)
	}
	for _, c := range n.children {
		addRepeated(m, c.name, toValue(c))
	}
	return m
}

func typedXML(s string) any {
	if numberRe.MatchString(s) && len(s) < 16 && !(len(s) > 1 && s[0] == '0' && s[1] != '.') {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return s
}

var isoNS = regexp.MustCompile(`urn:iso:std:iso:20022:tech:xsd:([a-z]{4}\.\d{3}\.\d{3}\.\d{2})`)

func isoMessageType(ns string) string {
	if m := isoNS.FindStringSubmatch(ns); m != nil {
		return m[1]
	}
	return ""
}

// ExternalStatusReason1Code subset (ISO 20022 external code list).
var isoReasons = map[string]string{
	"AC01": "IncorrectAccountNumber", "AC04": "ClosedAccountNumber", "AC06": "BlockedAccount",
	"AG01": "TransactionForbidden", "AG02": "InvalidBankOperationCode", "AM01": "ZeroAmount",
	"AM02": "NotAllowedAmount", "AM04": "InsufficientFunds", "AM05": "Duplication",
	"BE01": "InconsistentWithEndCustomer", "BE04": "MissingCreditorAddress", "DUPL": "DuplicatePayment",
	"FF01": "InvalidFileFormat", "MD01": "NoMandate", "MS02": "NotSpecifiedReasonCustomerGenerated",
	"MS03": "NotSpecifiedReasonAgentGenerated", "RC01": "BankIdentifierIncorrect",
	"RR01": "MissingDebtorAccountOrIdentification", "RR02": "MissingDebtorNameOrAddress",
	"RR03": "MissingCreditorNameOrAddress", "RR04": "RegulatoryReason", "NARR": "Narrative",
}

func summarizeISO(out map[string]any, root *xnode, msgType string) {
	out["messageType"] = msgType
	out["type"] = msgType[:8] // e.g. pacs.002

	if v := findText(root, "TxSts"); v != "" {
		out["status"] = v
	} else if v := findText(root, "GrpSts"); v != "" {
		out["status"] = v
	} else if v := findText(root, "TxGrpSts"); v != "" {
		out["status"] = v
	}
	if rsn := find(root, "Rsn"); rsn != nil {
		if code := findText(rsn, "Cd"); code != "" {
			out["reasonCode"] = code
			if name, ok := isoReasons[code]; ok {
				out["reasonName"] = name
			}
		}
	}
	if info := findText(root, "AddtlInf"); info != "" {
		out["additionalInfo"] = info
	}
	for _, tag := range []string{"IntrBkSttlmAmt", "InstdAmt", "TtlIntrBkSttlmAmt", "Amt"} {
		if n := find(root, tag); n != nil {
			if f, err := strconv.ParseFloat(strings.TrimSpace(n.text.String()), 64); err == nil {
				out["amount"] = f
				if ccy := n.attrs["Ccy"]; ccy != "" {
					out["currency"] = ccy
				}
				break
			}
		}
	}
	for key, tag := range map[string]string{"endToEndId": "EndToEndId", "uetr": "UETR", "msgId": "MsgId",
		"originalMsgId": "OrgnlMsgId", "settlementDate": "IntrBkSttlmDt"} {
		if v := findText(root, tag); v != "" {
			out[key] = v
		}
	}
	if v := findText(root, "OrgnlEndToEndId"); v != "" && out["endToEndId"] == nil {
		out["endToEndId"] = v
	}
	if d := find(root, "Dbtr"); d != nil {
		if v := findText(d, "Nm"); v != "" {
			out["debtor"] = v
		}
	}
	if c := find(root, "Cdtr"); c != nil {
		if v := findText(c, "Nm"); v != "" {
			out["creditor"] = v
		}
	}
	if a := find(root, "DbtrAcct"); a != nil {
		if v := findText(a, "IBAN"); v != "" {
			out["debtorIban"] = v
		}
	}
	if a := find(root, "CdtrAcct"); a != nil {
		if v := findText(a, "IBAN"); v != "" {
			out["creditorIban"] = v
		}
	}
	switch out["status"] {
	case "RJCT":
		out["severity"] = "error"
	case "PDNG":
		out["severity"] = "warning"
	default:
		out["severity"] = "info"
	}
}

// find returns the first descendant (depth-first) named name.
func find(n *xnode, name string) *xnode {
	for _, c := range n.children {
		if c.name == name {
			return c
		}
		if f := find(c, name); f != nil {
			return f
		}
	}
	return nil
}

func findText(n *xnode, name string) string {
	if f := find(n, name); f != nil {
		return strings.TrimSpace(f.text.String())
	}
	return ""
}
