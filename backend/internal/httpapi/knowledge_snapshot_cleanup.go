package httpapi

import (
	"context"
	"time"
)

const snapshotCleanupLock int64 = 716042902

const snapshotUnreferencedSQL = `
 NOT EXISTS(SELECT 1 FROM knowledge_mounts m WHERE m.snapshot_id=ks.id)
 AND NOT EXISTS(SELECT 1 FROM agent_versions av WHERE av.knowledge_snapshots @> jsonb_build_object(ks.kb_id,ks.id))
 AND NOT EXISTS(SELECT 1 FROM runs r WHERE r.input->'knowledge_snapshots' @> jsonb_build_object(ks.kb_id,ks.id))
 AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.citations @> jsonb_build_array(jsonb_build_object('snapshot_id',ks.id)))`

// References, not age alone, decide what may be removed. A durable outbox
// separates the database deletion from retryable Qdrant/MinIO cleanup.
func (s *Server) RunKnowledgeSnapshotCleanup(ctx context.Context) {
	if s.knowledge == nil {
		return
	}
	days := s.cfg.SnapshotRetentionDays
	if days <= 0 {
		days = 30
	}
	timer := time.NewTicker(time.Hour)
	defer timer.Stop()
	for {
		work, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err := s.collectKnowledgeSnapshots(work, time.Now().Add(-time.Duration(days)*24*time.Hour))
		cancel()
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("snapshot cleanup deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (s *Server) collectKnowledgeSnapshots(ctx context.Context, cutoff time.Time) error {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, snapshotCleanupLock).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, `SELECT pg_advisory_unlock($1)`, snapshotCleanupLock); err != nil {
			_ = conn.Conn().Close(cleanup)
		}
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Publishers and mount changes lock the same KB rows before reading pins.
	// Skip an active publisher; never hold these locks across remote deletion.
	rows, err := tx.Query(ctx, `SELECT kb.id FROM knowledge_bases kb WHERE EXISTS(SELECT 1 FROM knowledge_snapshots ks WHERE ks.kb_id=kb.id AND ks.version<>kb.version AND ks.created_at<$1 AND `+snapshotUnreferencedSQL+`) ORDER BY kb.id LIMIT 100 FOR UPDATE SKIP LOCKED`, cutoff)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM knowledge_snapshots ks USING knowledge_bases kb
 WHERE ks.kb_id=kb.id AND kb.id=ANY($1) AND ks.version<>kb.version AND ks.created_at<$2
 AND `+snapshotUnreferencedSQL, ids, cutoff); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	rows, err = conn.Query(ctx, `SELECT snapshot_id FROM knowledge_snapshot_cleanup WHERE next_attempt_at<=NOW() ORDER BY next_attempt_at LIMIT 100`)
	if err != nil {
		return err
	}
	pending := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range pending {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_snapshots WHERE id=$1)`, id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		work, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.knowledge.DiscardSnapshot(work, id)
		cancel()
		if err != nil {
			if _, updateErr := conn.Exec(ctx, `UPDATE knowledge_snapshot_cleanup SET attempts=attempts+1,next_attempt_at=NOW()+INTERVAL '1 hour' WHERE snapshot_id=$1`, id); updateErr != nil {
				return updateErr
			}
			s.logger.Warn("snapshot storage cleanup pending", "snapshot_id", id)
			continue
		}
		if _, err := conn.Exec(ctx, `DELETE FROM knowledge_snapshot_cleanup WHERE snapshot_id=$1`, id); err != nil {
			return err
		}
	}
	return nil
}
