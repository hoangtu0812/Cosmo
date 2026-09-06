package httpapi

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

var errRuntimeCapacity = errors.New("Hàng đợi đang đầy. Vui lòng thử lại sau.")

// All Chat/workflow admissions take this lock after their resource lock.
// Receipts are checked first so a retry remains readable at full capacity.
func (s *Server) checkRuntimeCapacity(ctx context.Context, tx pgx.Tx, workspace string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(716042903)`); err != nil {
		return err
	}
	globalLimit, localLimit := s.cfg.RuntimeQueueLimit, s.cfg.WorkspaceQueueLimit
	if globalLimit <= 0 {
		globalLimit = 1000
	}
	if localLimit <= 0 {
		localLimit = 50
	}
	var total, local int
	err := tx.QueryRow(ctx, `WITH active AS (
 SELECT r.workspace_id FROM chat_turns c JOIN runs r ON r.id=c.run_id WHERE c.status IN ('queued','executing','waiting_approval')
 UNION ALL SELECT workspace_id FROM workflow_executions WHERE status IN ('queued','running','waiting_approval'))
 SELECT count(*),count(*) FILTER(WHERE workspace_id=$1) FROM active`, workspace).Scan(&total, &local)
	if err != nil {
		return err
	}
	if total >= globalLimit || local >= localLimit {
		return errRuntimeCapacity
	}
	return nil
}

func (s *Server) RunRuntimeCleanup(ctx context.Context) {
	days := s.cfg.RuntimeRetentionDays
	if days <= 0 {
		days = 30
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for ctx.Err() == nil {
		work, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.pruneRuntimeHistory(work, time.Now().Add(-time.Duration(days)*24*time.Hour))
		cancel()
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("runtime retention deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Bounded payload pruning keeps receipts, accounting, messages, unresolved
// operations and resumable checkpoints. No remote deletion is performed.
func (s *Server) pruneRuntimeHistory(ctx context.Context, cutoff time.Time) error {
	queries := []string{
		`DELETE FROM knowledge_document_events WHERE id IN (SELECT e.id FROM knowledge_document_events e JOIN knowledge_documents d ON d.id=e.document_id WHERE e.created_at<$1 AND d.status IN ('ready','failed') AND NOT EXISTS(SELECT 1 FROM knowledge_ingestion_jobs j WHERE j.kb_id=d.kb_id AND j.status IN ('uploading','queued','running')) AND e.id<>(SELECT MAX(latest.id) FROM knowledge_document_events latest WHERE latest.document_id=d.id) ORDER BY e.id LIMIT 1000)`,
		`DELETE FROM chat_turn_events WHERE id IN (SELECT e.id FROM chat_turn_events e JOIN chat_turns c ON c.conversation_id=e.conversation_id AND c.client_message_id=e.client_message_id WHERE c.status IN ('succeeded','interrupted') AND c.finished_at<$1 ORDER BY e.id LIMIT 1000)`,
		`DELETE FROM workflow_execution_events WHERE id IN (SELECT e.id FROM workflow_execution_events e JOIN workflow_executions w ON w.id=e.execution_id WHERE w.status IN ('succeeded','failed','cancelled') AND w.finished_at<$1 ORDER BY e.id LIMIT 1000)`,
		`UPDATE chat_turns SET request_payload='{}',readable_ids='{}' WHERE (conversation_id,client_message_id) IN (SELECT conversation_id,client_message_id FROM chat_turns WHERE status IN ('succeeded','interrupted') AND finished_at<$1 AND (request_payload<>'{}'::jsonb OR cardinality(readable_ids)>0) LIMIT 100)`,
		`UPDATE workflow_executions w SET input='',completed=COALESCE((SELECT jsonb_object_agg(key,value-'output') FROM jsonb_each(w.completed)),'{}') WHERE w.id IN (SELECT id FROM workflow_executions WHERE status IN ('succeeded','failed','cancelled') AND finished_at<$1 AND (input<>'' OR completed @? '$.*.output') AND NOT EXISTS(SELECT 1 FROM tool_approvals a WHERE a.id=approval_id AND a.status IN ('pending','approved','uncertain')) LIMIT 100)`,
		`UPDATE knowledge_ingestion_jobs SET manifest='{}' WHERE id IN (SELECT id FROM knowledge_ingestion_jobs WHERE status IN ('succeeded','failed') AND finished_at<$1 AND manifest<>'{}' LIMIT 100)`,
		`UPDATE knowledge_snapshot_jobs SET manifest='{}' WHERE id IN (SELECT id FROM knowledge_snapshot_jobs WHERE status IN ('succeeded','failed') AND finished_at<$1 AND manifest<>'{}' LIMIT 100)`,
	}
	for _, query := range queries {
		if _, err := s.db.Exec(ctx, query, cutoff); err != nil {
			return err
		}
	}
	return nil
}
