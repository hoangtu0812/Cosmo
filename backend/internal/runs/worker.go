package runs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler func(context.Context, Job) error

type WorkerOptions struct {
	Owner        string
	PollInterval time.Duration
	Lease        time.Duration
}

type Worker struct {
	db       *pgxpool.Pool
	logger   *slog.Logger
	owner    string
	poll     time.Duration
	lease    time.Duration
	handlers map[string]Handler
}

type permanentError struct{ error }

func Permanent(err error) error { return permanentError{err} }

func NewWorker(db *pgxpool.Pool, logger *slog.Logger, options WorkerOptions) *Worker {
	if options.Owner == "" {
		options.Owner = newID("wrk_")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 500 * time.Millisecond
	}
	if options.Lease <= 0 {
		options.Lease = 30 * time.Second
	}
	return &Worker{db: db, logger: logger, owner: options.Owner, poll: options.PollInterval, lease: options.Lease, handlers: map[string]Handler{}}
}

func (worker *Worker) Handle(jobType string, handler Handler) { worker.handlers[jobType] = handler }

func (worker *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(worker.poll)
	defer ticker.Stop()
	for {
		job, found, err := worker.claim(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			worker.logger.Error("worker claim failed", "error", err)
		} else if found {
			worker.execute(ctx, job)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (worker *Worker) claim(ctx context.Context) (Job, bool, error) {
	tx, err := worker.db.Begin(ctx)
	if err != nil {
		return Job{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var job Job
	err = tx.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM worker_jobs
		WHERE attempt < max_attempts AND (
			(status = 'queued' AND available_at <= NOW()) OR
			(status = 'running' AND lease_expires_at < NOW())
		)
		ORDER BY available_at, id
		FOR UPDATE SKIP LOCKED LIMIT 1
	)
	UPDATE worker_jobs j SET status = 'running', lease_owner = $1,
		lease_expires_at = NOW() + $2::interval, attempt = attempt + 1, updated_at = NOW()
	FROM candidate WHERE j.id = candidate.id
	RETURNING j.id, COALESCE(j.run_id, ''), j.type, j.payload, j.attempt, j.max_attempts,
		j.lease_owner, j.lease_expires_at`, worker.owner, worker.lease.String()).Scan(
		&job.ID, &job.RunID, &job.Type, &job.Payload, &job.Attempt, &job.MaxAttempts, &job.LeaseOwner, &job.LeaseExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

func (worker *Worker) execute(parent context.Context, job Job) {
	handler, ok := worker.handlers[job.Type]
	if !ok {
		worker.finish(job, Permanent(fmt.Errorf("no handler registered for %s", job.Type)))
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan struct{})
	go worker.heartbeat(ctx, job.ID, done)
	err := handler(ctx, job)
	close(done)
	worker.finish(job, err)
}

func (worker *Worker) heartbeat(ctx context.Context, jobID int64, done <-chan struct{}) {
	interval := worker.lease / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			_, _ = worker.db.Exec(ctx, `UPDATE worker_jobs SET lease_expires_at = NOW() + $3::interval, updated_at = NOW()
				WHERE id = $1 AND lease_owner = $2 AND status = 'running'`, jobID, worker.owner, worker.lease.String())
		}
	}
}

func (worker *Worker) finish(job Job, handlerErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if handlerErr == nil {
		_, err := worker.db.Exec(ctx, `UPDATE worker_jobs SET status = 'succeeded', lease_owner = '', lease_expires_at = NULL,
			finished_at = NOW(), updated_at = NOW(), error_message = '' WHERE id = $1 AND lease_owner = $2`, job.ID, worker.owner)
		if err != nil {
			worker.logger.Error("worker completion failed", "job_id", job.ID, "error", err)
		}
		return
	}
	var permanent permanentError
	final := errors.As(handlerErr, &permanent) || job.Attempt >= job.MaxAttempts
	if final {
		_, _ = worker.db.Exec(ctx, `UPDATE worker_jobs SET status = 'failed', lease_owner = '', lease_expires_at = NULL,
			finished_at = NOW(), updated_at = NOW(), error_message = $3 WHERE id = $1 AND lease_owner = $2`, job.ID, worker.owner, handlerErr.Error())
		return
	}
	exponent := math.Min(float64(job.Attempt-1), 6)
	delay := time.Duration(math.Pow(2, exponent)) * time.Second
	_, _ = worker.db.Exec(ctx, `UPDATE worker_jobs SET status = 'queued', lease_owner = '', lease_expires_at = NULL,
		available_at = NOW() + $3::interval, updated_at = NOW(), error_message = $4 WHERE id = $1 AND lease_owner = $2`,
		job.ID, worker.owner, delay.String(), handlerErr.Error())
}
