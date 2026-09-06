package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const EffectRead = "read"
const EffectApproval = "approval"
const EffectBlocked = "blocked"

var ErrApprovalRequired = errors.New("Thao tác này cần xác nhận trước khi thực hiện. Kiểm tra đích và tham số trước khi xác nhận.")
var ErrActionBlocked = errors.New("Action đã bị chặn bởi chính sách tool.")
var ErrWriteUncertain = errors.New("Chưa xác định kết quả thao tác. Không gửi lại; hãy kiểm tra hệ thống đích và đối soát.")
var ErrWriteKey = errors.New("Mã thao tác không hợp lệ hoặc đã được dùng với nội dung khác.")
var ErrWritePolicy = errors.New("Chính sách tool không hợp lệ.")

type confirmedWriteKey struct{}
type approvalHandlerKey struct{}

// ApprovalHandler is installed only by the authenticated execution boundary.
// A model's arguments never grant permission to dispatch a write.
type ApprovalHandler func(context.Context, Tool, Action, map[string]any) (CallResult, error)

func WithApprovalHandler(ctx context.Context, handler ApprovalHandler) context.Context {
	return context.WithValue(ctx, approvalHandlerKey{}, handler)
}

func definitionHash(tool Tool, action Action) string {
	// Canonicalize JSONB and omit credentials, while binding fixed parameters,
	// schema, destination, authentication scheme and tool revision to review.
	var contract any
	_ = json.Unmarshal(action.MCPTool, &contract)
	raw, _ := json.Marshal([]any{tool.ID, tool.Kind, tool.BaseURL, tool.AuthType, tool.AuthHeaderName, tool.AuthHint, tool.UpdatedAt, action.ID, action.Name, action.Method, action.Path, action.Parameters, contract})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func defaultEffect(tool Tool, action Action) string {
	if tool.Kind == KindBuiltin {
		return EffectRead
	}
	if tool.Kind != KindMCP && (action.Method == "GET" || action.Method == "HEAD" || action.Method == "OPTIONS") {
		return EffectRead
	}
	return EffectApproval
}

func (repository *Repository) ActionEffect(ctx context.Context, tool Tool, action Action) (string, error) {
	effect := defaultEffect(tool, action)
	if repository.db == nil {
		return effect, nil
	}
	var configured, hash string
	err := repository.db.QueryRow(ctx, `SELECT effect,definition_hash FROM tool_action_policies WHERE action_id=$1 AND tool_id=$2`, action.ID, tool.ID).Scan(&configured, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return effect, nil
	}
	if err != nil {
		return "", err
	}
	if configured == EffectBlocked || hash == definitionHash(tool, action) {
		return configured, nil
	}
	// Even a formerly approved GET returns to review after its contract changes.
	return EffectApproval, nil
}

func (repository *Repository) SetActionEffect(ctx context.Context, tool Tool, action Action, userID, effect string) error {
	if effect != EffectRead && effect != EffectApproval && effect != EffectBlocked {
		return ErrWritePolicy
	}
	tx, err := repository.lockTool(ctx, tool.ID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	current, err := (&Repository{db: tx}).Get(ctx, tool.ID, userID, tool.WorkspaceID)
	if err != nil {
		return err
	}
	latest, err := (&Repository{db: tx}).Action(ctx, tool.ID, action.ID)
	if err != nil {
		return err
	}
	if !current.IsEditable || definitionHash(current, latest) != definitionHash(tool, action) {
		return ErrApprovalRequired
	}
	_, err = tx.Exec(ctx, `INSERT INTO tool_action_policies(action_id,tool_id,definition_hash,effect,updated_by) VALUES($1,$2,$3,$4,$5) ON CONFLICT(action_id) DO UPDATE SET definition_hash=EXCLUDED.definition_hash,effect=EXCLUDED.effect,updated_by=EXCLUDED.updated_by,updated_at=NOW()`, action.ID, tool.ID, definitionHash(tool, action), effect, userID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (repository *Repository) invokeAutomatic(ctx context.Context, tool Tool, action Action, args map[string]any) (CallResult, error) {
	effect, err := repository.ActionEffect(ctx, tool, action)
	if err != nil {
		return CallResult{}, err
	}
	if effect == EffectBlocked {
		return CallResult{}, ErrActionBlocked
	}
	if effect != EffectRead {
		if handler, ok := ctx.Value(approvalHandlerKey{}).(ApprovalHandler); ok {
			return handler(ctx, tool, action, args)
		}
		return CallResult{}, ErrApprovalRequired
	}
	return repository.Invoke(ctx, tool, action, args)
}

type WriteOperation struct {
	ID        string         `json:"id"`
	Key       string         `json:"idempotency_key"`
	ActionID  string         `json:"action_id"`
	Status    string         `json:"status"`
	Result    CallResult     `json:"result"`
	CreatedAt time.Time      `json:"created_at"`
	Note      string         `json:"reconciliation_note"`
	Request   map[string]any `json:"request"`
}

// Confirmed writes are dispatched at most once per actor/workspace key.
// Neither an unknown network outcome nor a process restart authorizes replay.
func (repository *Repository) InvokeConfirmed(ctx context.Context, tool Tool, action Action, args map[string]any, confirmed bool, key, expectedDefinition string) (CallResult, *WriteOperation, error) {
	effect, err := repository.ActionEffect(ctx, tool, action)
	if err != nil {
		return CallResult{}, nil, err
	}
	if effect == EffectBlocked {
		return CallResult{}, nil, ErrActionBlocked
	}
	if effect == EffectRead && !confirmed {
		result, err := repository.Invoke(ctx, tool, action, args)
		return result, nil, err
	}
	if !confirmed || expectedDefinition != definitionHash(tool, action) {
		return CallResult{}, nil, ErrApprovalRequired
	}
	caller, ok := CallerFrom(ctx)
	if !ok || repository.db == nil {
		return CallResult{}, nil, ErrApprovalRequired
	}
	if len(key) < 16 || len(key) > 100 || strings.TrimSpace(key) != key {
		return CallResult{}, nil, ErrWriteKey
	}
	encoded, err := json.Marshal(args)
	if err != nil || len(encoded) > MaxArgumentBytes {
		return CallResult{}, nil, ErrArguments
	}
	if tool.Kind == KindMCP {
		if err := validateMCPArguments(action, args); err != nil {
			return CallResult{}, nil, err
		}
	}
	digest := sha256.Sum256(append([]byte(definitionHash(tool, action)), encoded...))
	hash := hex.EncodeToString(digest[:])
	operation := WriteOperation{ID: newID("two_"), Key: key, ActionID: action.ID, Status: "executing"}
	operation.Request = map[string]any{"destination": tool.BaseURL, "action": action.Name, "method": action.Method, "path": action.Path, "arguments": args, "parameters": action.Parameters, "definition": expectedDefinition}
	reviewPayload, err := json.Marshal(operation.Request)
	if err != nil {
		return CallResult{}, nil, ErrArguments
	}
	tx, err := repository.db.Begin(ctx)
	if err != nil {
		return CallResult{}, nil, err
	}
	defer tx.Rollback(ctx)
	// Coordinate with tool edits/deletion and policy changes through admission.
	var locked string
	if err = tx.QueryRow(ctx, `SELECT id FROM tools WHERE id=$1 FOR SHARE`, tool.ID).Scan(&locked); err != nil {
		return CallResult{}, nil, err
	}
	current, err := (&Repository{db: tx}).Get(ctx, tool.ID, caller.UserID, caller.WorkspaceID)
	if err != nil {
		return CallResult{}, nil, err
	}
	latest, err := (&Repository{db: tx}).Action(ctx, tool.ID, action.ID)
	if err != nil {
		return CallResult{}, nil, err
	}
	if !current.IsEditable || definitionHash(current, latest) != expectedDefinition {
		return CallResult{}, nil, ErrApprovalRequired
	}
	effect, err = (&Repository{db: tx}).ActionEffect(ctx, current, latest)
	if err != nil {
		return CallResult{}, nil, err
	}
	if effect == EffectBlocked {
		return CallResult{}, nil, ErrActionBlocked
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tool.ID+":"+action.ID+":"+caller.UserID+":"+caller.WorkspaceID); err != nil {
		return CallResult{}, nil, err
	}
	var unresolved bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tool_write_operations WHERE tool_id=$1 AND action_id=$2 AND actor_id=$3 AND workspace_id=$4 AND status IN ('executing','uncertain') AND idempotency_key<>$5)`, tool.ID, action.ID, caller.UserID, caller.WorkspaceID, key).Scan(&unresolved); err != nil {
		return CallResult{}, nil, err
	}
	if unresolved {
		return CallResult{}, nil, ErrWriteUncertain
	}
	tag, err := tx.Exec(ctx, `INSERT INTO tool_write_operations(id,tool_id,action_id,actor_id,workspace_id,idempotency_key,request_hash,status,request) VALUES($1,$2,$3,$4,$5,$6,$7,'executing',$8) ON CONFLICT(actor_id,workspace_id,idempotency_key) DO NOTHING`, operation.ID, tool.ID, action.ID, caller.UserID, caller.WorkspaceID, key, hash, string(reviewPayload))
	if err != nil {
		return CallResult{}, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CallResult{}, nil, err
	}
	if tag.RowsAffected() == 0 {
		var storedHash string
		var raw []byte
		err = repository.db.QueryRow(ctx, `SELECT id,action_id,status,result,created_at,reconciliation_note,request_hash FROM tool_write_operations WHERE actor_id=$1 AND workspace_id=$2 AND idempotency_key=$3`, caller.UserID, caller.WorkspaceID, key).Scan(&operation.ID, &operation.ActionID, &operation.Status, &raw, &operation.CreatedAt, &operation.Note, &storedHash)
		if err != nil {
			return CallResult{}, nil, err
		}
		if storedHash != hash {
			return CallResult{}, nil, ErrWriteKey
		}
		if err = json.Unmarshal(raw, &operation.Result); err != nil {
			return CallResult{}, &operation, err
		}
		if operation.Status == "succeeded" {
			return operation.Result, &operation, nil
		}
		return operation.Result, &operation, ErrWriteUncertain
	}
	// Use a bounded context independent of the HTTP subscriber after admission.
	work, cancel := context.WithTimeout(context.WithValue(context.WithoutCancel(ctx), confirmedWriteKey{}, true), CallTimeout)
	defer cancel()
	result, callErr := repository.Invoke(work, tool, action, args)
	operation.Status = "uncertain"
	if callErr == nil && result.Status >= 200 && result.Status < 300 && result.Status != 202 {
		operation.Status = "succeeded"
	}
	operation.Result = result
	finish, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	raw, encodeErr := json.Marshal(result)
	if encodeErr != nil {
		return result, &operation, ErrWriteUncertain
	}
	_, err = repository.db.Exec(finish, `UPDATE tool_write_operations SET status=$2,result=$3,finished_at=NOW() WHERE id=$1 AND status='executing'`, operation.ID, operation.Status, string(raw))
	if err != nil || operation.Status != "succeeded" {
		return result, &operation, ErrWriteUncertain
	}
	return result, &operation, nil
}

func (repository *Repository) PolicyReview(tool Tool, action Action) string {
	return definitionHash(tool, action)
}

func (repository *Repository) WriteOperations(ctx context.Context, toolID, userID, workspaceID string) ([]WriteOperation, error) {
	rows, err := repository.db.Query(ctx, `SELECT id,idempotency_key,action_id,CASE WHEN status='executing' AND created_at<NOW()-INTERVAL '1 minute' THEN 'uncertain' ELSE status END,result,created_at,reconciliation_note,request FROM tool_write_operations WHERE tool_id=$1 AND actor_id=$2 AND workspace_id=$3 ORDER BY (status IN ('executing','uncertain')) DESC,created_at DESC LIMIT 50`, toolID, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	operations := []WriteOperation{}
	for rows.Next() {
		var op WriteOperation
		var raw []byte
		var request []byte
		if err = rows.Scan(&op.ID, &op.Key, &op.ActionID, &op.Status, &raw, &op.CreatedAt, &op.Note, &request); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &op.Result); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(request, &op.Request); err != nil {
			return nil, err
		}
		operations = append(operations, op)
	}
	return operations, rows.Err()
}

func (repository *Repository) ReconcileWrite(ctx context.Context, toolID, userID, workspaceID, id, outcome, note string) error {
	if (outcome != "reconciled_succeeded" && outcome != "reconciled_no_effect") || len([]rune(strings.TrimSpace(note))) < 5 || len([]rune(note)) > 1000 {
		return ErrWritePolicy
	}
	tag, err := repository.db.Exec(ctx, `UPDATE tool_write_operations SET status=$5,reconciliation_note=$6,finished_at=NOW() WHERE id=$1 AND tool_id=$2 AND actor_id=$3 AND workspace_id=$4 AND (status='uncertain' OR (status='executing' AND created_at<NOW()-INTERVAL '1 minute'))`, id, toolID, userID, workspaceID, outcome, strings.TrimSpace(note))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrWriteUncertain
	}
	return nil
}
