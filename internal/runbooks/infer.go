package runbooks

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Infer reads one written instruction and guesses what it asks for: a manual
// check, plain text, or an action Kardan can run with parameters taken from the
// alert. Guesses are marked Inferred so people confirm them in the preview,
// and risky actions still need approval whatever the text says.
func Infer(text string) Step {
	t := strings.TrimSpace(text)
	lc := strings.ToLower(t)
	s := Step{Kind: KindText, Text: t}
	if approvalRe.MatchString(t) {
		s.Approval = "required"
	}
	act := func(action string, params map[string]string) Step {
		s.Kind, s.Action, s.Params, s.Inferred = KindAction, action, params, true
		return s
	}
	if m := everyRe.FindStringSubmatch(lc); m != nil {
		s.Every = wordDuration(m[1], m[2]).String()
	}

	switch {
	case reWait.MatchString(lc):
		m := durationRe.FindStringSubmatch(lc)
		if m != nil {
			return act("wait", map[string]string{"duration": wordDuration(m[1], m[2]).String()})
		}
	case reRestart.MatchString(lc):
		return act("restart_service", map[string]string{"service": serviceRef(t)})
	case rePage.MatchString(lc) && reOnCall.MatchString(lc):
		team := "{{$team}}"
		if m := reOnCallTeam.FindStringSubmatch(lc); m != nil && !fillerWord[m[1]] {
			team = m[1]
		}
		return act("escalate", map[string]string{"to": "oncall:" + team, "channels": "phone"})
	case reEscalate.MatchString(lc):
		to := "team:{{$team}}"
		if m := reEscalateTo.FindStringSubmatch(lc); m != nil && !fillerWord[m[1]] {
			to = "team:" + m[1]
		}
		return act("escalate", map[string]string{"to": to})
	case reTicket.MatchString(lc):
		return act("open_ticket", map[string]string{"summary": "{{$title}}", "team": "{{$team}}"})
	case reChannel.MatchString(t) && reNotify.MatchString(lc):
		ch := reChannel.FindString(t)
		return act("notify_channel", map[string]string{"channel": ch, "message": "Update on {{$title}}: {{$status}}"})
	case reRisk.MatchString(lc):
		return act("notify_risk", map[string]string{"summary": "{{$title}}", "desk": "{{$desk}}"})
	case rePause.MatchString(lc):
		return act("pause_trading", map[string]string{"desk": "{{$desk}}"})
	case reResubmit.MatchString(lc):
		return act("resubmit_payment", map[string]string{"endToEndId": "{{endToEndId}}"})
	case reFailover.MatchString(lc):
		if reFeed.MatchString(lc) {
			return act("failover_feed", map[string]string{"instrument": "{{$instrument}}"})
		}
		return act("failover_region", map[string]string{"service": "{{$service}}"})
	case reRollback.MatchString(lc):
		return act("rollback_deployment", map[string]string{"service": serviceRef(t)})
	case reRetry.MatchString(lc):
		return act("retry_workflow", map[string]string{"workflowId": "{{workflowId}}"})
	}
	if u := urlRe.FindString(t); u != "" && reCheck.MatchString(lc) {
		s.Kind, s.Action, s.Params, s.Inferred = KindCheck, "http_get", map[string]string{"url": u}, true
		return s
	}
	if reCheck.MatchString(lc) {
		s.Kind = KindCheck
	}
	return s
}

var (
	approvalRe = regexp.MustCompile(`(?i)\b(needs?|requires?|with|get|after)\b[^.]{0,20}\b(approval|sign-?off|go-?ahead)\b|\bapproval (from|by)\b|\bapproved by\b|\bfour[- ]eyes\b|\bmaker[- ]checker\b`)
	durationRe = regexp.MustCompile(`\b(\d+|an?|one|two|three|four|five|ten|fifteen|twenty|thirty|sixty)\s*(s|secs?|seconds?|m|mins?|minutes?|h|hrs?|hours?)\b`)
	everyRe    = regexp.MustCompile(`\bevery\s+(\d+|an?|one|two|three|five|ten|fifteen|twenty|thirty|sixty)?\s*(s|secs?|seconds?|m|mins?|minutes?|h|hrs?|hours?)\b`)

	reWait       = regexp.MustCompile(`^(?:then\s+)?(?:wait|pause for|give it|hold for|sleep)\b`)
	reRestart    = regexp.MustCompile(`\b(restart|reboot|bounce|recycle)\b`)
	rePage       = regexp.MustCompile(`\b(call|page|phone|ring|wake up|contact)\b`)
	reOnCall     = regexp.MustCompile(`\bon[- ]?call\b`)
	reOnCallTeam = regexp.MustCompile(`\b([a-z][\w-]*)\s+on[- ]?call\b`)
	reEscalate   = regexp.MustCompile(`\bescalate\b`)
	reEscalateTo = regexp.MustCompile(`\bescalate\b(?:\s+(?:it|this|the alert|the incident))?\s+to\s+(?:the\s+)?([a-z][\w-]*)`)
	reTicket     = regexp.MustCompile(`\b(open|raise|create|file|log|cut)\b.{0,20}\b(ticket|jira|incident|case|servicenow)\b`)
	reChannel    = regexp.MustCompile(`#[A-Za-z0-9][\w-]*`)
	reNotify     = regexp.MustCompile(`\b(post|send|share|give|notify|message|update|announce|tell)\b`)
	reRisk       = regexp.MustCompile(`\b(notify|inform|tell|alert|call|email)\b.{0,20}\brisk\b`)
	rePause      = regexp.MustCompile(`\b(pause|halt|stop|suspend|block|freeze)\b.{0,25}\b(trading|desk|algo|orders)\b|\bkill[- ]switch\b`)
	reResubmit   = regexp.MustCompile(`\b(resubmit|re-submit|resend|re-send|release|repair and resubmit)\b.{0,25}\bpayments?\b`)
	reFailover   = regexp.MustCompile(`\bfail(?:\s|-)?(?:\S+\s){0,3}?over\b|\bfailover\b|\bswitch (?:traffic|over)\b|\bmove traffic\b`)
	reFeed       = regexp.MustCompile(`\b(feed|vendor|market data|price|prices|quotes?|ticks?)\b`)
	reRollback   = regexp.MustCompile(`\broll(?:\s|-)?(?:\w+\s)?back\b|\brollback\b|\brevert the (?:deploy|release|change)`)
	reRetry      = regexp.MustCompile(`\b(retry|re-?run|replay|resume|restart the workflow)\b`)
	reCheck      = regexp.MustCompile(`\b(check|verify|confirm|look at|look for|open|review|inspect|make sure|ensure|see if|compare|investigate)\b`)

	reFromAlert = regexp.MustCompile(`(?i)\b(named|mentioned|listed|shown|given|reported) in the alert\b|\bfrom the alert\b|\bthe alert's\b|\baffected\b|\bfailing\b|\b(that|this|the same) (instance|service|host)\b`)
	reService   = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:-[a-z0-9]+)+\b`)

	fillerWord = map[string]bool{"the": true, "an": true, "a": true, "their": true, "our": true, "your": true,
		"current": true, "relevant": true, "team": true, "next": true, "other": true}

	imperativeVerbs = map[string]bool{}
)

func init() {
	for _, v := range strings.Fields(`restart reboot bounce recycle wait call page phone ring wake escalate open raise create
		file log cut post send share give notify message inform tell check verify confirm look review inspect ensure make
		fail failover switch move roll rollback revert retry rerun re-run replay resume resubmit resend release repair
		pause halt stop suspend block freeze kill run clear flush scale increase decrease contact email ask update disable
		enable unblock reroute purge drain cancel reconcile compare investigate monitor watch follow go use mark reach
		delete remove add apply rotate renew restore reprocess rebook amend correct cancel hold sleep page ping alert
		document record note write`) {
		imperativeVerbs[v] = true
	}
}

// imperative reports whether a clause starts with an instruction verb.
func imperative(c string) bool {
	f := strings.Fields(strings.ToLower(strings.TrimSpace(c)))
	for len(f) > 0 && (f[0] == "please" || f[0] == "just" || f[0] == "immediately" || f[0] == "then") {
		f = f[1:]
	}
	if len(f) == 0 {
		return false
	}
	return imperativeVerbs[strings.Trim(f[0], ".,;:!")]
}

// serviceRef picks the service a step talks about: the alert's own service
// when the text says so ("the instance named in the alert"), else a name
// written in the step (payments-api), else the alert's service.
func serviceRef(text string) string {
	if reFromAlert.MatchString(text) {
		return "{{$service}}"
	}
	if s := reService.FindString(strings.ToLower(text)); s != "" && s != "on-call" && s != "follow-the-sun" {
		return s
	}
	return "{{$service}}"
}

var numberWords = map[string]int{"a": 1, "an": 1, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "ten": 10,
	"fifteen": 15, "twenty": 20, "thirty": 30, "sixty": 60, "": 1}

func wordDuration(n, unit string) time.Duration {
	v, ok := numberWords[n]
	if !ok {
		v, _ = strconv.Atoi(n)
	}
	switch {
	case strings.HasPrefix(unit, "h"):
		return time.Duration(v) * time.Hour
	case unit == "s" || strings.HasPrefix(unit, "sec"):
		return time.Duration(v) * time.Second
	default:
		return time.Duration(v) * time.Minute
	}
}

// Attempts reads "fails twice" / "fails 3 times" in a condition: how many
// times the previous action should be tried before the condition holds.
func Attempts(cond string) int {
	lc := strings.ToLower(cond)
	if !strings.Contains(lc, "fail") {
		return 0
	}
	switch {
	case strings.Contains(lc, "twice"):
		return 2
	case strings.Contains(lc, "three times") || strings.Contains(lc, "thrice"):
		return 3
	}
	if m := regexp.MustCompile(`(\d+)\s*times`).FindStringSubmatch(lc); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 10 {
			return n
		}
	}
	return 1
}

// OnFailure reports whether a condition means "the previous action failed".
func OnFailure(cond string) bool {
	lc := strings.ToLower(cond)
	return strings.Contains(lc, "fail") || strings.Contains(lc, "does not work") ||
		strings.Contains(lc, "doesn't work") || strings.Contains(lc, "unsuccessful")
}
