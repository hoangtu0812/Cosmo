package runs

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

func newID(prefix string) string {
	data := make([]byte, 18)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(data)
}

func jsonValue(value any) ([]byte, error) {
	if value == nil {
		return []byte(`{}`), nil
	}
	return json.Marshal(value)
}

func (repository *Repository) Create(ctx context.Context, input NewRun) (Run, bool, error) {
	if strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.ResourceType) == "" || strings.TrimSpace(input.ResourceID) == "" {
		return Run{}, false, errors.New("workspace, resource type and resource id are required")
	}
	if input.TriggerType == "" {
		input.TriggerType = "manual"
	}
	if input.TraceID == "" {
		input.TraceID = newID("trc_")
	}
	payload, err := jsonValue(input.Input)
	if err != nil {
		return Run{}, false, err
	}
	tx, err := repository.db.Begin(ctx)
	if err != nil {
		return Run{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if input.IdempotencyKey != "" {
		existing, findErr := getRun(ctx, tx, `SELECT `+runColumns+` FROM runs WHERE workspace_id = $1 AND idempotency_key = $2`, input.WorkspaceID, input.IdempotencyKey)
		if findErr == nil {
			return existing, false, nil
		}
		if !errors.Is(findErr, pgx.ErrNoRows) {
			return Run{}, false, findErr
		}
	}
	runID := newID("run_")
	_, err = tx.Exec(ctx, `INSERT INTO runs(
		id, workspace_id, project_id, actor_user_id, trigger_type, resource_type, resource_id,
		resource_version, input, idempotency_key, trace_id
	) VALUES($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11)`,
		runID, input.WorkspaceID, input.ProjectID, input.ActorUserID, input.TriggerType,
		input.ResourceType, input.ResourceID, input.ResourceVersion, payload, input.IdempotencyKey, input.TraceID)
	if err != nil {
		if input.IdempotencyKey != "" {
			existing, findErr := repository.GetByIdempotency(ctx, input.WorkspaceID, input.IdempotencyKey)
			if findErr == nil {
				return existing, false, nil
			}
		}
		return Run{}, false, err
	}
	if _, err = appendEventTx(ctx, tx, runID, "", "run.queued", map[string]any{"trigger_type": input.TriggerType}); err != nil {
		return Run{}, false, err
	}
	if input.Job != nil {
		if err = enqueueTx(ctx, tx, runID, *input.Job); err != nil {
			return Run{}, false, err
		}
	}
	created, err := getRun(ctx, tx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, runID)
	if err != nil {
		return Run{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, false, err
	}
	return created, true, nil
}

const runColumns = `id, workspace_id, project_id, COALESCE(actor_user_id, ''), trigger_type,
	resource_type, resource_id, resource_version, status, input, output, error_code,
	error_message, idempotency_key, trace_id, created_at, queued_at, started_at, finished_at, cancelled_at`

type rowScanner interface{ Scan(...any) error }

func scanRun(row rowScanner) (Run, error) {
	var run Run
	err := row.Scan(&run.ID, &run.WorkspaceID, &run.ProjectID, &run.ActorUserID, &run.TriggerType,
		&run.ResourceType, &run.ResourceID, &run.ResourceVersion, &run.Status, &run.Input, &run.Output,
		&run.ErrorCode, &run.ErrorMessage, &run.IdempotencyKey, &run.TraceID, &run.CreatedAt,
		&run.QueuedAt, &run.StartedAt, &run.FinishedAt, &run.CancelledAt)
	return run, err
}

func getRun(ctx context.Context, queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, query string, args ...any) (Run, error) {
	return scanRun(queryer.QueryRow(ctx, query, args...))
}

func (repository *Repository) Get(ctx context.Context, runID string) (Run, error) {
	return getRun(ctx, repository.db, `SELECT `+runColumns+` FROM runs WHERE id = $1`, runID)
}

func (repository *Repository) GetByIdempotency(ctx context.Context, workspaceID, key string) (Run, error) {
	return getRun(ctx, repository.db, `SELECT `+runColumns+` FROM runs WHERE workspace_id = $1 AND idempotency_key = $2`, workspaceID, key)
}

func (repository *Repository) List(ctx context.Context, workspaceID string, limit int) ([]Run, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := repository.db.Query(ctx, `SELECT `+runColumns+` FROM runs WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT $2`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Run{}
	for rows.Next() {
		run, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (repository *Repository) Transition(ctx context.Context, runID string, to Status, output any, errorCode, errorMessage string) (Run, error) {
	payload, err := jsonValue(output)
	if err != nil {
		return Run{}, err
	}
	tx, err := repository.db.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := getRun(ctx, tx, `SELECT `+runColumns+` FROM runs WHERE id = $1 FOR UPDATE`, runID)
	if err != nil {
		return Run{}, err
	}
	if !CanTransition(current.Status, to) {
		return Run{}, fmt.Errorf("%w: %s to %s", ErrInvalidTransition, current.Status, to)
	}
	_, err = tx.Exec(ctx, `UPDATE runs SET status = $2, output = $3, error_code = $4, error_message = $5,
		started_at = CASE WHEN $2 = 'running' THEN COALESCE(started_at, NOW()) ELSE started_at END,
		finished_at = CASE WHEN $2 IN ('succeeded', 'failed', 'cancelled', 'timed_out') THEN NOW() ELSE finished_at END,
		cancelled_at = CASE WHEN $2 = 'cancelled' THEN NOW() ELSE cancelled_at END
		WHERE id = $1`, runID, to, payload, errorCode, errorMessage)
	if err != nil {
		return Run{}, err
	}
	if _, err = appendEventTx(ctx, tx, runID, "", "run."+string(to), map[string]any{"error_code": errorCode, "error_message": errorMessage}); err != nil {
		return Run{}, err
	}
	updated, err := getRun(ctx, tx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, runID)
	if err != nil {
		return Run{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return updated, nil
}

func (repository *Repository) Cancel(ctx context.Context, runID string) (Run, error) {
	run, err := repository.Transition(ctx, runID, Cancelled, nil, "cancelled", "cancelled by user")
	if err != nil {
		return Run{}, err
	}
	_, _ = repository.db.Exec(ctx, `UPDATE worker_jobs SET status = 'cancelled', finished_at = NOW(), updated_at = NOW()
		WHERE run_id = $1 AND status IN ('queued', 'running')`, runID)
	return run, nil
}

func appendEventTx(ctx context.Context, tx pgx.Tx, runID, stepID, eventType string, payload any) (Event, error) {
	encoded, err := jsonValue(payload)
	if err != nil {
		return Event{}, err
	}
	var sequence int64
	if err = tx.QueryRow(ctx, `UPDATE runs SET next_event_sequence = next_event_sequence + 1 WHERE id = $1 RETURNING next_event_sequence - 1`, runID).Scan(&sequence); err != nil {
		return Event{}, err
	}
	var event Event
	err = tx.QueryRow(ctx, `INSERT INTO run_events(run_id, step_id, sequence, type, payload)
		VALUES($1, NULLIF($2, ''), $3, $4, $5)
		RETURNING id, run_id, COALESCE(step_id, ''), sequence, type, payload, created_at`,
		runID, stepID, sequence, eventType, encoded).Scan(&event.ID, &event.RunID, &event.StepID, &event.Sequence, &event.Type, &event.Payload, &event.CreatedAt)
	return event, err
}

func (repository *Repository) AppendEvent(ctx context.Context, runID, stepID, eventType string, payload any) (Event, error) {
	tx, err := repository.db.Begin(ctx)
	if err != nil {
		return Event{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	event, err := appendEventTx(ctx, tx, runID, stepID, eventType, payload)
	if err != nil {
		return Event{}, err
	}
	return event, tx.Commit(ctx)
}

func (repository *Repository) Events(ctx context.Context, runID string, after int64, limit int) ([]Event, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	rows, err := repository.db.Query(ctx, `SELECT id, run_id, COALESCE(step_id, ''), sequence, type, payload, created_at
		FROM run_events WHERE run_id = $1 AND sequence > $2 ORDER BY sequence LIMIT $3`, runID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Event{}
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.RunID, &event.StepID, &event.Sequence, &event.Type, &event.Payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

const stepColumns = `id, run_id, node_id, type, name, attempt, status, input, output,
	output_ref, timeout_ms, error_code, error_message, created_at, started_at, finished_at`

func scanStep(row rowScanner) (Step, error) {
	var step Step
	err := row.Scan(&step.ID, &step.RunID, &step.NodeID, &step.Type, &step.Name, &step.Attempt,
		&step.Status, &step.Input, &step.Output, &step.OutputRef, &step.TimeoutMS,
		&step.ErrorCode, &step.ErrorMessage, &step.CreatedAt, &step.StartedAt, &step.FinishedAt)
	return step, err
}

func (repository *Repository) CreateStep(ctx context.Context, input NewStep) (Step, error) {
	if input.RunID == "" || input.Type == "" {
		return Step{}, errors.New("run id and step type are required")
	}
	if input.Attempt < 1 {
		input.Attempt = 1
	}
	payload, err := jsonValue(input.Input)
	if err != nil {
		return Step{}, err
	}
	tx, err := repository.db.Begin(ctx)
	if err != nil {
		return Step{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stepID := newID("stp_")
	step, err := scanStep(tx.QueryRow(ctx, `INSERT INTO run_steps(id, run_id, node_id, type, name, attempt, input, timeout_ms)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+stepColumns,
		stepID, input.RunID, input.NodeID, input.Type, input.Name, input.Attempt, payload, input.TimeoutMS))
	if err != nil {
		return Step{}, err
	}
	if _, err = appendEventTx(ctx, tx, input.RunID, stepID, "step.queued", map[string]any{"type": input.Type, "name": input.Name}); err != nil {
		return Step{}, err
	}
	return step, tx.Commit(ctx)
}

func (repository *Repository) Steps(ctx context.Context, runID string) ([]Step, error) {
	rows, err := repository.db.Query(ctx, `SELECT `+stepColumns+` FROM run_steps WHERE run_id = $1 ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Step{}
	for rows.Next() {
		step, scanErr := scanStep(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, step)
	}
	return result, rows.Err()
}

func (repository *Repository) TransitionStep(ctx context.Context, stepID string, to Status, output any, outputRef, errorCode, errorMessage string) (Step, error) {
	payload, err := jsonValue(output)
	if err != nil {
		return Step{}, err
	}
	tx, err := repository.db.Begin(ctx)
	if err != nil {
		return Step{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanStep(tx.QueryRow(ctx, `SELECT `+stepColumns+` FROM run_steps WHERE id = $1 FOR UPDATE`, stepID))
	if err != nil {
		return Step{}, err
	}
	if !CanTransition(current.Status, to) {
		return Step{}, fmt.Errorf("%w: %s to %s", ErrInvalidTransition, current.Status, to)
	}
	updated, err := scanStep(tx.QueryRow(ctx, `UPDATE run_steps SET status = $2, output = $3, output_ref = $4,
		error_code = $5, error_message = $6,
		started_at = CASE WHEN $2 = 'running' THEN COALESCE(started_at, NOW()) ELSE started_at END,
		finished_at = CASE WHEN $2 IN ('succeeded', 'failed', 'cancelled', 'timed_out') THEN NOW() ELSE finished_at END
		WHERE id = $1 RETURNING `+stepColumns, stepID, to, payload, outputRef, errorCode, errorMessage))
	if err != nil {
		return Step{}, err
	}
	if _, err = appendEventTx(ctx, tx, current.RunID, stepID, "step."+string(to), map[string]any{"error_code": errorCode, "error_message": errorMessage}); err != nil {
		return Step{}, err
	}
	return updated, tx.Commit(ctx)
}

func enqueueTx(ctx context.Context, tx pgx.Tx, runID string, job NewJob) error {
	if strings.TrimSpace(job.Type) == "" {
		return errors.New("job type is required")
	}
	payload, err := jsonValue(job.Payload)
	if err != nil {
		return err
	}
	if job.MaxAttempts < 1 {
		job.MaxAttempts = 5
	}
	_, err = tx.Exec(ctx, `INSERT INTO worker_jobs(run_id, type, payload, dedupe_key, max_attempts)
		VALUES(NULLIF($1, ''), $2, $3, $4, $5) ON CONFLICT (dedupe_key) WHERE dedupe_key <> '' DO NOTHING`,
		runID, job.Type, payload, job.DedupeKey, job.MaxAttempts)
	return err
}
