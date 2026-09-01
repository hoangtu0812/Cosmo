package runs

import (
	"encoding/json"
	"errors"
	"time"
)

type Status string

const (
	Queued          Status = "queued"
	Running         Status = "running"
	WaitingApproval Status = "waiting_approval"
	Succeeded       Status = "succeeded"
	Failed          Status = "failed"
	Cancelled       Status = "cancelled"
	TimedOut        Status = "timed_out"
)

var ErrInvalidTransition = errors.New("invalid run status transition")

func (status Status) Terminal() bool {
	return status == Succeeded || status == Failed || status == Cancelled || status == TimedOut
}

func CanTransition(from, to Status) bool {
	allowed := map[Status]map[Status]bool{
		Queued:          {Running: true, Cancelled: true, TimedOut: true},
		Running:         {WaitingApproval: true, Succeeded: true, Failed: true, Cancelled: true, TimedOut: true},
		WaitingApproval: {Running: true, Failed: true, Cancelled: true, TimedOut: true},
	}
	return allowed[from][to]
}

type Run struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspace_id"`
	ProjectID       string          `json:"project_id,omitempty"`
	ActorUserID     string          `json:"actor_user_id,omitempty"`
	TriggerType     string          `json:"trigger_type"`
	ResourceType    string          `json:"resource_type"`
	ResourceID      string          `json:"resource_id"`
	ResourceVersion string          `json:"resource_version,omitempty"`
	Status          Status          `json:"status"`
	Input           json.RawMessage `json:"input"`
	Output          json.RawMessage `json:"output"`
	ErrorCode       string          `json:"error_code,omitempty"`
	ErrorMessage    string          `json:"error_message,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	TraceID         string          `json:"trace_id"`
	CreatedAt       time.Time       `json:"created_at"`
	QueuedAt        time.Time       `json:"queued_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	CancelledAt     *time.Time      `json:"cancelled_at,omitempty"`
}

type NewRun struct {
	WorkspaceID     string
	ProjectID       string
	ActorUserID     string
	TriggerType     string
	ResourceType    string
	ResourceID      string
	ResourceVersion string
	Input           any
	IdempotencyKey  string
	TraceID         string
	Job             *NewJob
}

type Step struct {
	ID           string          `json:"id"`
	RunID        string          `json:"run_id"`
	NodeID       string          `json:"node_id,omitempty"`
	Type         string          `json:"type"`
	Name         string          `json:"name,omitempty"`
	Attempt      int             `json:"attempt"`
	Status       Status          `json:"status"`
	Input        json.RawMessage `json:"input"`
	Output       json.RawMessage `json:"output"`
	OutputRef    string          `json:"output_ref,omitempty"`
	TimeoutMS    int64           `json:"timeout_ms"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`
}

type NewStep struct {
	RunID     string
	NodeID    string
	Type      string
	Name      string
	Attempt   int
	Input     any
	TimeoutMS int64
}

type Event struct {
	ID        int64           `json:"id"`
	RunID     string          `json:"run_id"`
	StepID    string          `json:"step_id,omitempty"`
	Sequence  int64           `json:"sequence"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type NewJob struct {
	Type        string
	Payload     any
	DedupeKey   string
	MaxAttempts int
}

type Job struct {
	ID           int64           `json:"id"`
	RunID        string          `json:"run_id,omitempty"`
	Type         string          `json:"type"`
	Payload      json.RawMessage `json:"payload"`
	Attempt      int             `json:"attempt"`
	MaxAttempts  int             `json:"max_attempts"`
	LeaseOwner   string          `json:"lease_owner"`
	LeaseExpires time.Time       `json:"lease_expires_at"`
}
