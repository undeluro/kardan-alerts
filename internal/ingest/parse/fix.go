package parse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// FIX parses FIX tag=value messages (SOH-, pipe- or caret-delimited, as they
// appear in engine logs) into named fields, e.g.
//
//	8=FIX.4.4|35=8|39=8|55=EURUSD|54=1|38=25000000|44=1.0842|15=EUR|58=Credit limit exceeded
//
// becomes {"MsgType":"8","msgType":"ExecutionReport","OrdStatus":"8","OrdStatusName":"Rejected",
// "Symbol":"EURUSD","SideName":"Buy","OrderQty":25000000,...,"notional":27105000,"severity":"error"}.
//
// Known tags get their FIX names; unknown tags stay addressable as tags.<number>.
// Repeating tags become arrays.
type FIX struct{}

var fixStart = regexp.MustCompile(`8=FIXT?\.\d`)

func looksLikeFIX(b []byte) bool {
	return fixStart.Match(b) || (bytes.Contains(b, []byte("35=")) && bytes.Count(b, []byte("=")) >= 3 &&
		(bytes.IndexByte(b, 0x01) >= 0 || bytes.Count(b, []byte("|")) >= 2))
}

var fixTags = map[string]string{
	"1": "Account", "6": "AvgPx", "8": "BeginString", "9": "BodyLength", "10": "CheckSum", "11": "ClOrdID",
	"14": "CumQty", "15": "Currency", "17": "ExecID", "21": "HandlInst", "22": "SecurityIDSource",
	"30": "LastMkt", "31": "LastPx", "32": "LastQty", "34": "MsgSeqNum", "35": "MsgType", "37": "OrderID",
	"38": "OrderQty", "39": "OrdStatus", "40": "OrdType", "41": "OrigClOrdID", "43": "PossDupFlag",
	"44": "Price", "45": "RefSeqNum", "48": "SecurityID", "49": "SenderCompID", "52": "SendingTime",
	"54": "Side", "55": "Symbol", "56": "TargetCompID", "58": "Text", "59": "TimeInForce", "60": "TransactTime",
	"63": "SettlType", "64": "SettlDate", "97": "PossResend", "98": "EncryptMethod", "100": "ExDestination",
	"102": "CxlRejReason", "103": "OrdRejReason", "108": "HeartBtInt", "112": "TestReqID",
	"115": "OnBehalfOfCompID", "122": "OrigSendingTime", "128": "DeliverToCompID", "150": "ExecType",
	"151": "LeavesQty", "167": "SecurityType", "207": "SecurityExchange", "372": "RefMsgType",
	"373": "SessionRejectReason", "380": "BusinessRejectReason", "434": "CxlRejResponseTo",
	"447": "PartyIDSource", "448": "PartyID", "452": "PartyRole", "453": "NoPartyIDs", "461": "CFICode",
	"487": "TradeReportTransType", "571": "TradeReportID", "856": "TradeReportType", "1003": "TradeID",
	"1128": "ApplVerID",
}

var fixNumeric = map[string]bool{
	"6": true, "9": true, "14": true, "31": true, "32": true, "34": true, "38": true, "44": true, "45": true,
	"108": true, "151": true,
}

var fixEnums = map[string]map[string]string{
	"35": {"0": "Heartbeat", "1": "TestRequest", "2": "ResendRequest", "3": "Reject", "4": "SequenceReset",
		"5": "Logout", "8": "ExecutionReport", "9": "OrderCancelReject", "A": "Logon", "D": "NewOrderSingle",
		"F": "OrderCancelRequest", "G": "OrderCancelReplaceRequest", "j": "BusinessMessageReject",
		"AE": "TradeCaptureReport", "AR": "TradeCaptureReportAck"},
	"39": {"0": "New", "1": "PartiallyFilled", "2": "Filled", "3": "DoneForDay", "4": "Canceled",
		"5": "Replaced", "6": "PendingCancel", "7": "Stopped", "8": "Rejected", "9": "Suspended",
		"A": "PendingNew", "B": "Calculated", "C": "Expired", "E": "PendingReplace"},
	"150": {"0": "New", "4": "Canceled", "5": "Replaced", "6": "PendingCancel", "8": "Rejected",
		"A": "PendingNew", "C": "Expired", "E": "PendingReplace", "F": "Trade", "I": "OrderStatus"},
	"54": {"1": "Buy", "2": "Sell", "5": "SellShort", "6": "SellShortExempt"},
	"103": {"0": "BrokerOption", "1": "UnknownSymbol", "2": "ExchangeClosed", "3": "OrderExceedsLimit",
		"4": "TooLateToEnter", "5": "UnknownOrder", "6": "DuplicateOrder", "11": "UnsupportedOrderCharacteristic",
		"13": "IncorrectQuantity", "99": "Other"},
	"373": {"0": "InvalidTagNumber", "1": "RequiredTagMissing", "2": "TagNotDefinedForMessageType",
		"5": "ValueIncorrect", "6": "IncorrectDataFormat", "9": "CompIDProblem", "10": "SendingTimeAccuracyProblem",
		"11": "InvalidMsgType", "99": "Other"},
	"380": {"0": "Other", "1": "UnknownID", "2": "UnknownSecurity", "3": "UnsupportedMessageType",
		"4": "ApplicationNotAvailable", "5": "ConditionallyRequiredFieldMissing", "6": "NotAuthorized"},
}

func (FIX) Parse(raw []byte) (Result, error) {
	s := strings.TrimSpace(string(raw))
	// Engine logs often prefix the message with a timestamp; start at 8=FIX.
	if loc := fixStart.FindStringIndex(s); loc != nil && loc[0] > 0 {
		s = s[loc[0]:]
	}
	sep := "\x01"
	switch {
	case strings.Contains(s, "\x01"):
	case strings.Contains(s, "|"):
		sep = "|"
	case strings.Contains(s, "^A"):
		sep = "^A"
	default:
		sep = " "
	}

	out := map[string]any{}
	tags := map[string]any{}
	n := 0
	for _, field := range strings.Split(s, sep) {
		field = strings.TrimSpace(field)
		tag, val, ok := strings.Cut(field, "=")
		if !ok || tag == "" || !isDigits(tag) {
			continue
		}
		n++
		var v any = val
		if fixNumeric[tag] {
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				v = f
			}
		}
		addRepeated(tags, tag, v)
		if name, known := fixTags[tag]; known {
			addRepeated(out, name, v)
			if names, ok := fixEnums[tag]; ok {
				if label, ok := names[val]; ok {
					addRepeated(out, name+"Name", label)
				}
			}
		}
	}
	if n < 2 || out["MsgType"] == nil {
		return Result{}, fmt.Errorf("not a FIX message (no MsgType)")
	}
	out["tags"] = tags

	msgType, _ := out["MsgTypeName"].(string)
	if msgType == "" {
		msgType, _ = out["MsgType"].(string)
	}
	out["msgType"] = msgType
	out["type"] = "fix." + msgType

	// Finance conveniences: notional and severity in the shape other schemas use.
	if qty, ok := out["OrderQty"].(float64); ok {
		if px, ok := out["Price"].(float64); ok && px > 0 {
			out["notional"] = round2(qty * px)
		} else {
			out["notional"] = qty
		}
	}
	out["severity"] = fixSeverity(out)

	b, err := json.Marshal(out)
	return Result{Payload: b, Format: "fix", Raw: strings.ReplaceAll(s, "\x01", "|")}, err
}

func fixSeverity(m map[string]any) string {
	switch {
	case m["MsgType"] == "3" || m["MsgType"] == "j" || m["MsgType"] == "9":
		return "error"
	case m["OrdStatus"] == "8" || m["ExecType"] == "8":
		return "error"
	case m["MsgType"] == "5":
		return "warning" // logout
	default:
		return "info"
	}
}

func addRepeated(m map[string]any, k string, v any) {
	prev, ok := m[k]
	if !ok {
		m[k] = v
		return
	}
	if arr, isArr := prev.([]any); isArr {
		m[k] = append(arr, v)
		return
	}
	m[k] = []any{prev, v}
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
