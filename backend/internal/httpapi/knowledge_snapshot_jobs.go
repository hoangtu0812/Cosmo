package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"net/http"
	"time"
)

var errSnapshotLeaseLost = errors.New("snapshot job lease lost")
var errSnapshotPermission = errors.New("snapshot publisher no longer authorized")

func (s *Server) getSnapshotJob(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), currentUser(r.Context()).ID, kbID) != "owner" {
		writeError(w, 404, "Không tìm thấy tác vụ snapshot.")
		return
	}
	job, err := s.readSnapshotJob(r.Context(), chi.URLParam(r, "jobID"))
	if err != nil || job.KBID != kbID {
		writeError(w, 404, "Không tìm thấy tác vụ snapshot.")
		return
	}
	writeJSON(w, 200, map[string]any{"snapshot_job": job})
}

type snapshotJob struct {
	ID          string `json:"id"`
	KBID        string `json:"kb_id"`
	Status      string `json:"status"`
	SnapshotID  string `json:"snapshot_id,omitempty"`
	Version     int    `json:"version"`
	ErrorCode   string `json:"error_code,omitempty"`
	Manifest    string `json:"-"`
	AttemptID   string `json:"-"`
	Owner       string `json:"-"`
	RequestedBy string `json:"-"`
	Attempts    int    `json:"attempts"`
}

func (s *Server) enqueueSnapshot(ctx context.Context, kbID, userID string) (string, error) {
	if s.knowledge == nil {
		return "", fmt.Errorf("knowledge service not configured")
	}
	manifest, _, err := snapshotManifest(ctx, s.db, kbID)
	if err != nil {
		return "", err
	}
	var id string
	// Repeated clicks attach to the existing build; its captured manifest stays fixed.
	err = s.db.QueryRow(ctx, `INSERT INTO knowledge_snapshot_jobs(id,kb_id,requested_by,manifest) VALUES($1,$2,$3,$4)
 ON CONFLICT(kb_id) WHERE status IN ('queued','running') DO UPDATE SET kb_id=EXCLUDED.kb_id RETURNING id`, "ksj_"+randomID(18), kbID, userID, manifest).Scan(&id)
	return id, err
}

func (s *Server) readSnapshotJob(ctx context.Context, id string) (snapshotJob, error) {
	var job snapshotJob
	err := s.db.QueryRow(ctx, `SELECT id,kb_id,status,snapshot_id,version,error_code,attempts FROM knowledge_snapshot_jobs WHERE id=$1`, id).Scan(&job.ID, &job.KBID, &job.Status, &job.SnapshotID, &job.Version, &job.ErrorCode, &job.Attempts)
	return job, err
}

func (s *Server) waitSnapshotJob(ctx context.Context, id string) (snapshotJob, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := s.readSnapshotJob(ctx, id)
		if err != nil {
			return job, err
		}
		if job.Status == "succeeded" {
			return job, nil
		}
		if job.Status == "failed" {
			if job.ErrorCode == "manifest_changed" {
				return job, errSnapshotChanged
			}
			return job, fmt.Errorf("snapshot build failed: %s", job.ErrorCode)
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Server) claimSnapshotJob(ctx context.Context) (snapshotJob, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return snapshotJob{}, err
	}
	defer tx.Rollback(ctx)
	var job snapshotJob
	err = tx.QueryRow(ctx, `SELECT id,kb_id,manifest,attempt_id,COALESCE(requested_by,''),attempts FROM knowledge_snapshot_jobs WHERE (status='queued' AND next_attempt_at<=NOW()) OR (status='running' AND lease_expires_at<=NOW()) ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&job.ID, &job.KBID, &job.Manifest, &job.AttemptID, &job.RequestedBy, &job.Attempts)
	if err != nil {
		return job, err
	}
	if job.AttemptID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO knowledge_snapshot_cleanup(snapshot_id,next_attempt_at) VALUES($1,NOW()+INTERVAL '1 hour') ON CONFLICT DO NOTHING`, job.AttemptID); err != nil {
			return job, err
		}
	}
	if job.Attempts >= 3 {
		if _, err := tx.Exec(ctx, `UPDATE knowledge_snapshot_jobs SET status='failed',error_code='attempts_exhausted',finished_at=NOW(),lease_expires_at=NULL WHERE id=$1`, job.ID); err != nil {
			return job, err
		}
		if err := tx.Commit(ctx); err != nil {
			return job, err
		}
		return snapshotJob{}, pgx.ErrNoRows
	}
	sum := sha256.Sum256([]byte(randomID(32)))
	job.AttemptID = "kbs_" + hex.EncodeToString(sum[:16])
	job.Owner = randomID(24)
	job.Attempts++
	if _, err := tx.Exec(ctx, `UPDATE knowledge_snapshot_jobs SET status='running',attempts=attempts+1,attempt_id=$2,lease_owner=$3,lease_expires_at=NOW()+INTERVAL '6 minutes' WHERE id=$1`, job.ID, job.AttemptID, job.Owner); err != nil {
		return job, err
	}
	return job, tx.Commit(ctx)
}

func (s *Server) executeSnapshotJob(ctx context.Context, job snapshotJob) error {
	work, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var allowed bool
	err := s.db.QueryRow(work, `SELECT EXISTS(SELECT 1 FROM knowledge_bases kb JOIN users u ON u.id=$2 WHERE kb.id=$1 AND (u.role='admin' OR EXISTS(SELECT 1 FROM workspace_memberships m WHERE m.workspace_id=kb.owner_workspace_id AND m.user_id=u.id AND m.role IN ('owner','admin'))))`, job.KBID, job.RequestedBy).Scan(&allowed)
	permissionChecked := err == nil
	if err == nil && !allowed {
		err = errSnapshotPermission
	}
	if err == nil {
		_, _, err = s.buildKnowledgeSnapshot(work, job.KBID, &job)
	}
	if err == nil {
		return nil
	}
	finish, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	tx, finishErr := s.db.Begin(finish)
	if finishErr != nil {
		return finishErr
	}
	defer tx.Rollback(finish)
	status, code := "queued", "copy_failed"
	if errors.Is(err, errSnapshotChanged) {
		status, code = "failed", "manifest_changed"
	}
	if (permissionChecked && !allowed) || errors.Is(err, errSnapshotPermission) {
		status, code = "failed", "permission_revoked"
	}
	if job.Attempts >= 3 {
		status = "failed"
	}
	tag, finishErr := tx.Exec(finish, `UPDATE knowledge_snapshot_jobs SET status=$4,error_code=$5,next_attempt_at=NOW()+INTERVAL '10 seconds',lease_expires_at=NULL,finished_at=CASE WHEN $4='failed' THEN NOW() ELSE NULL END WHERE id=$1 AND lease_owner=$2 AND attempt_id=$3 AND status='running'`, job.ID, job.Owner, job.AttemptID, status, code)
	if finishErr != nil {
		return finishErr
	}
	if tag.RowsAffected() > 0 {
		if _, finishErr := tx.Exec(finish, `INSERT INTO knowledge_snapshot_cleanup(snapshot_id,next_attempt_at) VALUES($1,NOW()+INTERVAL '1 hour') ON CONFLICT DO NOTHING`, job.AttemptID); finishErr != nil {
			return finishErr
		}
	}
	return tx.Commit(finish)
}

func (s *Server) RunKnowledgeSnapshotWorker(ctx context.Context) {
	if s.knowledge == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		job, err := s.claimSnapshotJob(ctx)
		if err == nil {
			err = s.executeSnapshotJob(ctx, job)
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
			s.logger.Warn("snapshot worker deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
