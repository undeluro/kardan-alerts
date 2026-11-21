package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Run statuses: a runbook step's execution on an alert.
const (
	RunAwaitingApproval = "awaiting_approval" // waiting for a second person
	RunQueued           = "queued"            // ready (or scheduled: not_before) for the executor
	RunRunning          = "running"
	RunSucceeded        = "succeeded"
	RunFailed           = "failed"
	RunRejected         = "rejected"
	RunDone             = "done"    // a manual step someone ticked
	RunSkipped          = "skipped" // condition not met
)

// Run is one execution of a runbook step.
type Run struct {
	ID            string            `json:"id"`
	Code          string            `json:"code"` // for SMS replies: APPROVE <code>
	AlertID       string            `json:"alertId"`
	RunbookID     string            `json:"runbookId"`
	Step          int               `json:"step"`
	Kind          string            `json:"kind"`
	Action        string            `json:"action,omitempty"`
	Label         string            `json:"label"`
	Params        map[string]string `json:"params,omitempty"`
	Status        string            `json:"status"`
	Risk          string            `json:"risk"`
	Approval      string            `json:"approval"`
	RequestedBy   string            `json:"requestedBy"`
	RequestedByID string            `json:"requestedById,omitempty"`
	ApprovedBy    *string           `json:"approvedBy,omitempty"`
	ApprovedAt    *time.Time        `json:"approvedAt,omitempty"`
	DecidedBy     *string           `json:"decidedBy,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	Attempts      int               `json:"attempts"`
	MaxAttempts   int               `json:"maxAttempts"`
	RepeatEvery   int64             `json:"repeatEveryMs,omitempty"`
	NotBefore     time.Time         `json:"notBefore"`
	Notified      bool              `json:"-"`
	Output        string            `json:"output,omitempty"`
	CreatedAt     time.Time         `json:"createdAt"`
	UpdatedAt     time.Time         `json:"updatedAt"`
	FinishedAt    *time.Time        `json:"finishedAt,omitempty"`
}

// Final reports whether the run will not change any more.
func (r *Run) Final() bool {
	switch r.Status {
	case RunSucceeded, RunFailed, RunRejected, RunDone, RunSkipped:
		return true
	}
	return false
}

const runColumns = `id, alert_id, runbook_id, step, kind, action, label, params, status, risk, approval,
	requested_by, requested_by_id, approved_by, approved_at, decided_by, reason, attempts, max_attempts, repeat_every,
	not_before, notified, output, created_at, updated_at, finished_at`

func scanRun(row pgx.Row) (*Run, error) {
	var r Run
	var params []byte
	err := row.Scan(&r.ID, &r.AlertID, &r.RunbookID, &r.Step, &r.Kind, &r.Action, &r.Label, &params, &r.Status,
		&r.Risk, &r.Approval, &r.RequestedBy, &r.RequestedByID, &r.ApprovedBy, &r.ApprovedAt, &r.DecidedBy, &r.Reason, &r.Attempts,
		&r.MaxAttempts, &r.RepeatEvery, &r.NotBefore, &r.Notified, &r.Output, &r.CreatedAt, &r.UpdatedAt, &r.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(params, &r.Params)
	r.Code = ShortCode(r.ID)
	return &r, nil
}

func collectRuns(rows pgx.Rows, err error) ([]*Run, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Execer can run statements (a pool or a transaction).
type Execer interface {
	Querier
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// InsertRun records a new step execution.
func InsertRun(ctx context.Context, q Execer, r *Run) error {
	params, _ := json.Marshal(r.Params)
	if r.MaxAttempts < 1 {
		r.MaxAttempts = 1
	}
	if r.NotBefore.IsZero() {
		r.NotBefore = time.Now()
	}
	var finished *time.Time
	if r.Final() {
		now := time.Now()
		finished = &now
	}
	_, err := q.Exec(ctx, `INSERT INTO action_runs (id, alert_id, runbook_id, step, kind, action, label, params, status,
		risk, approval, requested_by, requested_by_id, decided_by, reason, max_attempts, repeat_every, not_before, output, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
		r.ID, r.AlertID, r.RunbookID, r.Step, r.Kind, r.Action, r.Label, params, r.Status, r.Risk, r.Approval,
		r.RequestedBy, r.RequestedByID, r.DecidedBy, r.Reason, r.MaxAttempts, r.RepeatEvery, r.NotBefore, r.Output, finished)
	r.Code = ShortCode(r.ID)
	return err
}

func GetRun(ctx context.Context, q Querier, id string) (*Run, error) {
	return scanRun(q.QueryRow(ctx, `SELECT `+runColumns+` FROM action_runs WHERE id = $1`, id))
}

// RunsForAlert lists every step execution on an alert, oldest first.
func RunsForAlert(ctx context.Context, q Querier, alertID string) ([]*Run, error) {
	return collectRuns(q.Query(ctx, `SELECT `+runColumns+` FROM action_runs WHERE alert_id = $1
		ORDER BY created_at, id`, alertID))
}

// ClaimRuns takes due runs for execution (and runs whose executor died) with
// a lease, so several workers never execute the same run at once.
func ClaimRuns(ctx context.Context, q Querier, limit int, lease time.Duration) ([]*Run, error) {
	return collectRuns(q.Query(ctx, `
		UPDATE action_runs SET status = 'running', attempts = attempts + 1, updated_at = now(),
		       lease_until = now() + $2::interval
		WHERE id IN (
		    SELECT id FROM action_runs
		    WHERE (status = 'queued' AND not_before <= now()) OR (status = 'running' AND lease_until < now())
		    ORDER BY not_before LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING `+runColumns, limit, lease.String()))
}

// FinishRun records the outcome; retryAt re-queues a failed attempt.
func FinishRun(ctx context.Context, q Querier, id, status, output string, retryAt *time.Time) (*Run, error) {
	if retryAt != nil {
		return scanRun(q.QueryRow(ctx, `UPDATE action_runs SET status = 'queued', output = $2, not_before = $3,
			lease_until = NULL, updated_at = now() WHERE id = $1 RETURNING `+runColumns, id, output, *retryAt))
	}
	return scanRun(q.QueryRow(ctx, `UPDATE action_runs SET status = $2, output = $3, lease_until = NULL,
		updated_at = now(), finished_at = now() WHERE id = $1 RETURNING `+runColumns, id, status, output))
}

// ErrNotAwaiting means the run was already decided (or never needed approval).
var ErrNotAwaiting = errors.New("this step is not waiting for approval (already decided?)")

// ApproveRun moves an awaiting run to the queue. The four-eyes check (the
// approver is not the requester) is the caller's job, before this.
func ApproveRun(ctx context.Context, q Querier, id, by string) (*Run, error) {
	r, err := scanRun(q.QueryRow(ctx, `UPDATE action_runs SET status = 'queued', approved_by = $2, approved_at = now(),
		not_before = now(), updated_at = now() WHERE id = $1 AND status = 'awaiting_approval' RETURNING `+runColumns, id, by))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotAwaiting
	}
	return r, err
}

func RejectRun(ctx context.Context, q Querier, id, by, reason string) (*Run, error) {
	r, err := scanRun(q.QueryRow(ctx, `UPDATE action_runs SET status = 'rejected', decided_by = $2, reason = $3,
		updated_at = now(), finished_at = now() WHERE id = $1 AND status = 'awaiting_approval' RETURNING `+runColumns,
		id, by, reason))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotAwaiting
	}
	return r, err
}

// AwaitingApproval lists runs waiting for a decision (optionally only those
// whose approvers have not been told yet).
func AwaitingApproval(ctx context.Context, q Querier, unnotifiedOnly bool) ([]*Run, error) {
	return collectRuns(q.Query(ctx, `SELECT `+runColumns+` FROM action_runs
		WHERE status = 'awaiting_approval' AND ($1 = false OR notified = false) ORDER BY created_at LIMIT 500`, unnotifiedOnly))
}

// MarkNotified records that approvers were told; false if another worker did it.
func MarkNotified(ctx context.Context, q Execer, id string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE action_runs SET notified = true WHERE id = $1 AND notified = false`, id)
	return tag.RowsAffected() == 1, err
}

// CancelOpenRuns stops pending work on a resolved alert (queued, scheduled and
// awaiting approval). Running steps finish.
func CancelOpenRuns(ctx context.Context, q Execer, alertID, why string) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE action_runs SET status = 'skipped', reason = $2, updated_at = now(), finished_at = now()
		WHERE alert_id = $1 AND status IN ('queued', 'awaiting_approval')`, alertID, why)
	return tag.RowsAffected(), err
}

// PendingApprovals counts awaiting runs per alert (for badges in the feed).
func PendingApprovals(ctx context.Context, q Querier, alertIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(alertIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT alert_id, count(*) FROM action_runs
		WHERE status = 'awaiting_approval' AND alert_id = ANY($1) GROUP BY alert_id`, alertIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
