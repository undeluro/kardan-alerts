package proposals

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RuleStats summarises how people actually handled one rule's alerts.
type RuleStats struct {
	RuleID          string
	Alerts          int
	SelfResolved    int // closed by Kardan (recovery/silence) before anyone acknowledged
	QuickHuman      int // closed by a person within 5 minutes of opening
	NeverAcked      int // still open after a day without acknowledgement
	MedianResolve   time.Duration
	Events          int64
	PhoneOrSMSSends int
}

// Signal is one finding about a rule, with the change it suggests.
type Signal struct {
	Rule      string
	Name      string // flapping | noise | ignored
	Title     string
	Rationale string
}

// Analyze looks at the last `days` of alerts per rule.
func Analyze(ctx context.Context, pool *pgxpool.Pool, days int) ([]RuleStats, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.rule_id,
		       count(*),
		       count(*) FILTER (WHERE a.status = 'resolved' AND a.acked_at IS NULL AND a.resolved_by LIKE 'auto:%'
		                          AND a.resolved_at - a.created_at < interval '10 minutes'),
		       count(*) FILTER (WHERE a.status = 'resolved' AND a.resolved_by NOT LIKE 'auto:%'
		                          AND a.resolved_at - a.created_at < interval '5 minutes'),
		       count(*) FILTER (WHERE a.status = 'open' AND a.created_at < now() - interval '1 day'),
		       coalesce(extract(epoch FROM percentile_cont(0.5) WITHIN GROUP (ORDER BY a.resolved_at - a.created_at)
		                FILTER (WHERE a.resolved_at IS NOT NULL)), 0),
		       coalesce(sum(a.count), 0),
		       (SELECT count(*) FROM notifications n JOIN alerts x ON x.id = n.alert_id
		         WHERE x.rule_id = a.rule_id AND n.channel IN ('sms', 'phone') AND x.created_at > now() - make_interval(days => $1))
		FROM alerts a
		WHERE a.created_at > now() - make_interval(days => $1)
		GROUP BY a.rule_id`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuleStats
	for rows.Next() {
		var st RuleStats
		var median float64
		if err := rows.Scan(&st.RuleID, &st.Alerts, &st.SelfResolved, &st.QuickHuman, &st.NeverAcked, &median,
			&st.Events, &st.PhoneOrSMSSends); err != nil {
			return nil, err
		}
		st.MedianResolve = time.Duration(median * float64(time.Second))
		out = append(out, st)
	}
	return out, rows.Err()
}

func pct(n, of int) int { return int(math.Round(100 * float64(n) / float64(of))) }

// Signals turns stats into findings. Thresholds are deliberately conservative:
// a suggestion needs at least `min` alerts behind it.
func Signals(stats []RuleStats, min int) []Signal {
	var out []Signal
	for _, st := range stats {
		if st.Alerts < min {
			continue
		}
		switch {
		case st.SelfResolved*2 >= st.Alerts:
			out = append(out, Signal{Rule: st.RuleID, Name: "flapping",
				Title: "Most alerts clear by themselves within minutes",
				Rationale: fmt.Sprintf("%d of %d alerts (%d%%) in the period closed on their own within 10 minutes, before anyone acknowledged them.",
					st.SelfResolved, st.Alerts, pct(st.SelfResolved, st.Alerts))})
		case st.QuickHuman*2 >= st.Alerts && st.PhoneOrSMSSends > 0:
			out = append(out, Signal{Rule: st.RuleID, Name: "noise",
				Title: "Lower the severity: people close these within minutes",
				Rationale: fmt.Sprintf("%d of %d alerts (%d%%) were resolved by a person within 5 minutes (median %s), and the rule sent %d SMS/phone notifications. "+
					"Quick, routine closes suggest the alert is informational; one level lower moves it off phone and SMS for people using default preferences.",
					st.QuickHuman, st.Alerts, pct(st.QuickHuman, st.Alerts), st.MedianResolve.Round(time.Second), st.PhoneOrSMSSends)})
		case st.NeverAcked*2 >= st.Alerts:
			out = append(out, Signal{Rule: st.RuleID, Name: "ignored",
				Title: "Nobody acts on these alerts",
				Rationale: fmt.Sprintf("%d of %d alerts (%d%%) are still open after a day without acknowledgement. "+
					"Either route them to someone who owns the problem or lower the severity so they stop competing with real incidents.",
					st.NeverAcked, st.Alerts, pct(st.NeverAcked, st.Alerts))})
		}
	}
	return out
}
