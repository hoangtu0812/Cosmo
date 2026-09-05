package httpapi

import (
	"context"
	"cosmo/backend/internal/knowledge"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"time"
)

var errIngestionBusy = errors.New("Knowledge Base đang xử lý tài liệu; hãy đợi hoàn tất.")
var errIngestionChanged = errors.New("Cấu hình hoặc tài liệu đã thay đổi; cần chạy lại re-index.")
var errIngestionLease = errors.New("ingestion lease lost")

type ingestionDocument struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Title       string `json:"title"`
	Version     int    `json:"version"`
	StorageKey  string `json:"storage_key"`
}
type ingestionManifestData struct {
	Layout    string              `json:"layout"`
	Documents []ingestionDocument `json:"documents"`
}
type ingestionJob struct {
	ID, KBID, RequestedBy, Manifest, AttemptID, Owner string
	Attempts                                          int
}

func ingestionManifest(ctx context.Context, db snapshotReader, kbID string) (string, error) {
	var raw string
	err := db.QueryRow(ctx, `SELECT jsonb_build_object('kb_updated',kb.updated_at,'gateway_updated',w.updated_at,'live_index',kb.live_index_id,'layout',kb.layout_mode,
 'documents',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',d.id,'filename',d.filename,'content_type',d.content_type,'title',d.title,'version',d.version,'storage_key',d.storage_key,'size_bytes',d.size_bytes) ORDER BY d.id) FROM knowledge_documents d WHERE d.kb_id=kb.id),'[]'::jsonb))::text
 FROM knowledge_bases kb LEFT JOIN workspace_llm_configs w ON w.workspace_id=kb.owner_workspace_id WHERE kb.id=$1`, kbID).Scan(&raw)
	return raw, err
}

// Caller holds the KB row lock and commits the complete admission transaction.
func enqueueIngestion(ctx context.Context, tx pgx.Tx, kbID, userID, status string) (string, error) {
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_ingestion_jobs WHERE kb_id=$1 AND status IN ('uploading','queued','running'))`, kbID).Scan(&active); err != nil {
		return "", err
	}
	if active {
		return "", errIngestionBusy
	}
	manifest, err := ingestionManifest(ctx, tx, kbID)
	if err != nil {
		return "", err
	}
	var parsed ingestionManifestData
	if err = json.Unmarshal([]byte(manifest), &parsed); err != nil {
		return "", err
	}
	if len(parsed.Documents) == 0 || len(parsed.Documents) > 10000 {
		return "", fmt.Errorf("invalid ingestion manifest size")
	}
	for _, doc := range parsed.Documents {
		if doc.StorageKey == "" {
			return "", fmt.Errorf("tài liệu chưa có bản gốc; hãy tải lại tệp")
		}
	}
	id := "kij_" + randomID(18)
	_, err = tx.Exec(ctx, `INSERT INTO knowledge_ingestion_jobs(id,kb_id,requested_by,manifest,status,next_attempt_at) VALUES($1,$2,$3,$4,$5,CASE WHEN $5='uploading' THEN NOW()+INTERVAL '2 minutes' ELSE NOW() END)`, id, kbID, userID, manifest, status)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE knowledge_documents SET status='processing',error='',updated_at=NOW() WHERE kb_id=$1`, kbID)
	return id, err
}

func (s *Server) ingestionTimeout() time.Duration {
	timeout := s.cfg.RAGTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if timeout > 30*time.Minute {
		timeout = 30 * time.Minute
	}
	return timeout
}

func (s *Server) claimIngestionJob(ctx context.Context) (ingestionJob, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ingestionJob{}, err
	}
	defer tx.Rollback(ctx)
	var job ingestionJob
	err = tx.QueryRow(ctx, `SELECT id,kb_id,COALESCE(requested_by,''),manifest,attempt_id,attempts FROM knowledge_ingestion_jobs
 WHERE (status IN ('uploading','queued') AND next_attempt_at<=NOW()) OR (status='running' AND lease_expires_at<=NOW())
 ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&job.ID, &job.KBID, &job.RequestedBy, &job.Manifest, &job.AttemptID, &job.Attempts)
	if err != nil {
		return job, err
	}
	// An expired third attempt is finalized through the same fenced failure path.
	if job.AttemptID != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO knowledge_snapshot_cleanup(snapshot_id,next_attempt_at) VALUES($1,NOW()+INTERVAL '1 hour') ON CONFLICT DO NOTHING`, job.AttemptID); err != nil {
			return job, err
		}
	}
	sum := sha256.Sum256([]byte(randomID(32)))
	job.AttemptID = "kbs_" + hex.EncodeToString(sum[:16])
	job.Owner = randomID(24)
	job.Attempts++
	_, err = tx.Exec(ctx, `UPDATE knowledge_ingestion_jobs SET status='running',attempts=$2,attempt_id=$3,lease_owner=$4,lease_expires_at=NOW()+$5*INTERVAL '1 second' WHERE id=$1`, job.ID, job.Attempts, job.AttemptID, job.Owner, int((s.ingestionTimeout() + time.Minute).Seconds()))
	if err != nil {
		return job, err
	}
	return job, tx.Commit(ctx)
}

func (s *Server) ingestionPermission(ctx context.Context, db snapshotReader, job ingestionJob) error {
	var allowed bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_bases kb JOIN users u ON u.id=$2 WHERE kb.id=$1 AND (u.role='admin' OR EXISTS(SELECT 1 FROM workspace_memberships m WHERE m.workspace_id=kb.owner_workspace_id AND m.user_id=u.id AND m.role IN ('owner','admin'))))`, job.KBID, job.RequestedBy).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return errSnapshotPermission
	}
	return nil
}

func (s *Server) buildIngestion(ctx context.Context, job ingestionJob) error {
	if job.Attempts > 3 {
		return fmt.Errorf("attempts exhausted")
	}
	if err := s.ingestionPermission(ctx, s.db, job); err != nil {
		return err
	}
	before, err := ingestionManifest(ctx, s.db, job.KBID)
	if err != nil {
		return err
	}
	if before != job.Manifest {
		return errIngestionChanged
	}
	settings, err := s.configuredKnowledgeModelSettings(ctx, job.KBID)
	if err != nil {
		return err
	}
	// Recheck after reading settings so a concurrent config change cannot bind
	// a new model to an old admission manifest.
	after, err := ingestionManifest(ctx, s.db, job.KBID)
	if err != nil {
		return err
	}
	if after != before {
		return errIngestionChanged
	}
	var manifest ingestionManifestData
	if err = json.Unmarshal([]byte(job.Manifest), &manifest); err != nil {
		return err
	}
	results := map[string]knowledge.IngestResult{}
	for _, doc := range manifest.Documents {
		var owned bool
		if err := s.db.QueryRow(ctx, `SELECT status='running' AND lease_owner=$2 AND attempt_id=$3 AND lease_expires_at>NOW() FROM knowledge_ingestion_jobs WHERE id=$1`, job.ID, job.Owner, job.AttemptID).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return errIngestionLease
		}
		record := func(event knowledge.Event) {
			if event.Terminal() {
				return
			}
			_, _ = s.db.Exec(ctx, `INSERT INTO knowledge_document_events(document_id,stage,message,done,total)
 SELECT $1,$2,$3,$4,$5 WHERE EXISTS(SELECT 1 FROM knowledge_ingestion_jobs WHERE id=$6 AND status='running' AND lease_owner=$7 AND attempt_id=$8 AND lease_expires_at>NOW())`, doc.ID, event.Stage, event.Message, event.Done, event.Total, job.ID, job.Owner, job.AttemptID)
		}
		result, err := s.knowledge.Ingest(ctx, knowledge.IngestJob{KBID: job.KBID, DocumentID: doc.ID, Filename: doc.Filename, ContentType: doc.ContentType, Title: doc.Title, Version: doc.Version, StorageKey: doc.StorageKey, LayoutMode: manifest.Layout, TargetSnapshotID: job.AttemptID}, settings, record)
		if err != nil {
			return err
		}
		if result.Chunks <= 0 || result.StorageKey != doc.StorageKey {
			return fmt.Errorf("ingestion result verification failed")
		}
		results[doc.ID] = result
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var old string
	if err = tx.QueryRow(ctx, `SELECT live_index_id FROM knowledge_bases WHERE id=$1 FOR UPDATE`, job.KBID).Scan(&old); err != nil {
		return err
	}
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT status='running' AND lease_owner=$2 AND attempt_id=$3 AND lease_expires_at>NOW() FROM knowledge_ingestion_jobs WHERE id=$1 FOR UPDATE`, job.ID, job.Owner, job.AttemptID).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return errIngestionLease
	}
	// Freeze document metadata and gateway revision through the pointer commit.
	locked, err := tx.Query(ctx, `SELECT id FROM knowledge_documents WHERE kb_id=$1 ORDER BY id FOR UPDATE`, job.KBID)
	if err != nil {
		return err
	}
	for locked.Next() {
		var id string
		if err := locked.Scan(&id); err != nil {
			locked.Close()
			return err
		}
	}
	locked.Close()
	if err := locked.Err(); err != nil {
		return err
	}
	var gatewayWorkspace string
	if err = tx.QueryRow(ctx, `SELECT w.workspace_id FROM workspace_llm_configs w JOIN knowledge_bases kb ON kb.owner_workspace_id=w.workspace_id WHERE kb.id=$1 FOR SHARE OF w`, job.KBID).Scan(&gatewayWorkspace); err != nil {
		return err
	}
	if err = s.ingestionPermission(ctx, tx, job); err != nil {
		return err
	}
	after, err = ingestionManifest(ctx, tx, job.KBID)
	if err != nil {
		return err
	}
	if after != before {
		return errIngestionChanged
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE knowledge_bases SET live_index_id=$2,live_index_settings=$3,updated_at=NOW() WHERE id=$1`, job.KBID, job.AttemptID, string(encoded)); err != nil {
		return err
	}
	for id, result := range results {
		if _, err = tx.Exec(ctx, `UPDATE knowledge_documents SET status='ready',chunk_count=$2,error='',updated_at=NOW() WHERE id=$1`, id, result.Chunks); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO knowledge_document_events(document_id,stage,message,done,total) VALUES($1,'done','Chỉ mục mới đã sẵn sàng.', $2,$2)`, id, result.Chunks); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE knowledge_ingestion_jobs SET status='succeeded',error_code='',finished_at=NOW(),lease_expires_at=NULL WHERE id=$1`, job.ID); err != nil {
		return err
	}
	if old != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO knowledge_snapshot_cleanup(snapshot_id,next_attempt_at) VALUES($1,NOW()+INTERVAL '1 hour') ON CONFLICT DO NOTHING`, old); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Server) executeIngestionJob(ctx context.Context, job ingestionJob) error {
	work, cancel := context.WithTimeout(ctx, s.ingestionTimeout())
	err := s.buildIngestion(work, job)
	cancel()
	if err == nil {
		return nil
	}
	// Resolve even uncertain commits with a conditional update. A succeeded job
	// never becomes failed or enters the cleanup outbox after a lost response.
	finish, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	tx, finishErr := s.db.Begin(finish)
	if finishErr != nil {
		return finishErr
	}
	defer tx.Rollback(finish)
	var kb string
	if finishErr = tx.QueryRow(finish, `SELECT id FROM knowledge_bases WHERE id=$1 FOR UPDATE`, job.KBID).Scan(&kb); finishErr != nil {
		return finishErr
	}
	status, code := "queued", "ingestion_failed"
	if job.Attempts >= 3 {
		status = "failed"
	}
	if errors.Is(err, errIngestionChanged) {
		status, code = "failed", "manifest_changed"
	}
	if errors.Is(err, errSnapshotPermission) {
		status, code = "failed", "permission_revoked"
	}
	tag, finishErr := tx.Exec(finish, `UPDATE knowledge_ingestion_jobs SET status=$4,error_code=$5,next_attempt_at=NOW()+INTERVAL '10 seconds',lease_expires_at=NULL,finished_at=CASE WHEN $4='failed' THEN NOW() ELSE NULL END WHERE id=$1 AND status='running' AND lease_owner=$2 AND attempt_id=$3`, job.ID, job.Owner, job.AttemptID, status, code)
	if finishErr != nil {
		return finishErr
	}
	if tag.RowsAffected() > 0 {
		if _, finishErr = tx.Exec(finish, `INSERT INTO knowledge_snapshot_cleanup(snapshot_id,next_attempt_at) VALUES($1,NOW()+INTERVAL '1 hour') ON CONFLICT DO NOTHING`, job.AttemptID); finishErr != nil {
			return finishErr
		}
		stage, message := "retry", "Xử lý tạm gián đoạn; tác vụ sẽ tự thử lại."
		if status == "failed" {
			stage, message = "error", "Không thể hoàn tất chỉ mục mới; giữ chỉ mục trước. Hãy kiểm tra cấu hình và chạy lại re-index."
			if _, finishErr = tx.Exec(finish, `UPDATE knowledge_documents SET status='failed',error=$2,updated_at=NOW() WHERE kb_id=$1 AND status='processing'`, job.KBID, message); finishErr != nil {
				return finishErr
			}
		}
		if _, finishErr = tx.Exec(finish, `INSERT INTO knowledge_document_events(document_id,stage,message,done,total) SELECT id,$2,$3,0,0 FROM knowledge_documents WHERE kb_id=$1`, job.KBID, stage, message); finishErr != nil {
			return finishErr
		}
	}
	return tx.Commit(finish)
}

func (s *Server) RunKnowledgeIngestionWorker(ctx context.Context) {
	if s.knowledge == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		job, err := s.claimIngestionJob(ctx)
		if err == nil {
			err = s.executeIngestionJob(ctx, job)
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
			s.logger.Warn("ingestion worker deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
