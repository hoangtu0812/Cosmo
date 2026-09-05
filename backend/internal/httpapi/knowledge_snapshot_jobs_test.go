package httpapi

import (
	"context"
	"cosmo/backend/internal/knowledge"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func snapshotJobFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	s, agent, owner, _ := agentAccessFixture(t)
	ctx := context.Background()
	kb := createRetrievalKB(t, s, agent.WorkspaceID, agent.WorkspaceID, "workspace", "embed")
	if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,'https://gateway.invalid','chat')`, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_documents(id,kb_id,title,filename,content_type,size_bytes,storage_key,status,chunk_count,uploaded_by) VALUES($1,$2,'Test','test.txt','text/plain',3,'live','ready',1,$3)`, "doc_"+randomID(18), kb, owner.ID); err != nil {
		t.Fatal(err)
	}
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		var body struct {
			ID        string                                `json:"snapshot_id"`
			Originals map[string]knowledge.SnapshotOriginal `json:"originals"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		for key, value := range body.Originals {
			value.StorageKey = "knowledge-snapshots/" + body.ID + "/" + key
			body.Originals[key] = value
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"snapshot_id": body.ID, "chunks": 1, "digest": strings.Repeat("a", 64), "originals": body.Originals})
	}))
	t.Cleanup(rag.Close)
	s.knowledge = knowledge.New(rag.URL, time.Second)
	return s, kb, owner.ID
}

func TestSnapshotJobRecoveryFencesOldWorkerAndAuditsOnce(t *testing.T) {
	s, kb, user := snapshotJobFixture(t)
	ctx := context.Background()
	id, err := s.enqueueSnapshot(ctx, kb, user)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.enqueueSnapshot(ctx, kb, user)
	if err != nil || duplicate != id {
		t.Fatalf("duplicate build: %s %v", duplicate, err)
	}
	old, err := s.claimSnapshotJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.claimSnapshotJob(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("double claim: %v", err)
	}
	// Simulate process death after remote storage may have been written.
	if _, err := s.db.Exec(ctx, `UPDATE knowledge_snapshot_jobs SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.claimSnapshotJob(ctx)
	if err != nil || fresh.AttemptID == old.AttemptID || fresh.Attempts != 2 {
		t.Fatalf("recovery: %+v %v", fresh, err)
	}
	if _, _, err := s.buildKnowledgeSnapshot(ctx, kb, &old); !errors.Is(err, errSnapshotLeaseLost) {
		t.Fatalf("stale worker published: %v", err)
	}
	if err := s.executeSnapshotJob(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	saved, err := s.readSnapshotJob(ctx, id)
	if err != nil || saved.Status != "succeeded" || saved.SnapshotID != fresh.AttemptID || saved.Version != 2 {
		t.Fatalf("result: %+v %v", saved, err)
	}
	// Replaying a completion must neither publish another version nor audit twice.
	if err := s.executeSnapshotJob(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE metadata->>'job_id'=$1`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit: %d %v", count, err)
	}
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM knowledge_snapshot_cleanup WHERE snapshot_id=$1`, old.AttemptID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("orphan not queued: %d %v", count, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.waitSnapshotJob(cancelled, id); err == nil {
		t.Fatal("cancelled wait succeeded")
	}
	saved, err = s.readSnapshotJob(ctx, id)
	if err != nil || saved.Status != "succeeded" {
		t.Fatal("subscriber cancellation changed build")
	}
}

func TestSnapshotJobRejectsChangedManifestAndRevokedPublisher(t *testing.T) {
	for _, mode := range []string{"manifest", "permission", "exhausted"} {
		t.Run(mode, func(t *testing.T) {
			s, kb, user := snapshotJobFixture(t)
			ctx := context.Background()
			id, err := s.enqueueSnapshot(ctx, kb, user)
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.claimSnapshotJob(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "manifest":
				_, err = s.db.Exec(ctx, `UPDATE knowledge_documents SET version=version+1 WHERE kb_id=$1`, kb)
			case "permission":
				_, err = s.db.Exec(ctx, `DELETE FROM workspace_memberships WHERE user_id=$1`, user)
			case "exhausted":
				_, err = s.db.Exec(ctx, `UPDATE knowledge_snapshot_jobs SET attempts=3,lease_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "exhausted" {
				_, err = s.claimSnapshotJob(ctx)
				if !errors.Is(err, pgx.ErrNoRows) {
					t.Fatal(err)
				}
			} else if err := s.executeSnapshotJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			result, err := s.readSnapshotJob(ctx, id)
			if err != nil || result.Status != "failed" {
				t.Fatalf("unsafe job: %+v %v", result, err)
			}
			var version int
			if err := s.db.QueryRow(ctx, `SELECT version FROM knowledge_bases WHERE id=$1`, kb).Scan(&version); err != nil || version != 1 {
				t.Fatalf("failed build moved release: %d %v", version, err)
			}
		})
	}
}

func TestSnapshotHTTPDisconnectDoesNotCancelQueuedBuild(t *testing.T) {
	s, kb, user := snapshotJobFixture(t)
	ctx := context.Background()
	if _, err := s.db.Exec(ctx, `UPDATE users SET last_workspace_id=(SELECT owner_workspace_id FROM knowledge_bases WHERE id=$2) WHERE id=$1`, user, kb); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Post("/knowledge/{kbID}/publish", s.publishKnowledgeBase)
	subscriber, disconnect := context.WithCancel(context.WithValue(ctx, userContextKey, User{ID: user}))
	defer disconnect()
	r := httptest.NewRequest("POST", "/knowledge/"+kb+"/publish", nil).WithContext(subscriber)
	w := httptest.NewRecorder()
	returned := make(chan struct{})
	go func() { defer close(returned); router.ServeHTTP(w, r) }()
	var id string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := s.db.QueryRow(ctx, `SELECT id FROM knowledge_snapshot_jobs WHERE kb_id=$1`, kb).Scan(&id); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("HTTP did not enqueue build")
	}
	disconnect()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not disconnect")
	}
	job, err := s.readSnapshotJob(ctx, id)
	if err != nil || job.Status != "queued" {
		t.Fatalf("disconnect changed job: %+v %v", job, err)
	}
	worker, stop := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() { defer close(stopped); s.RunKnowledgeSnapshotWorker(worker) }()
	defer func() { stop(); <-stopped }()
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	job, err = s.waitSnapshotJob(wait, id)
	if err != nil || job.Status != "succeeded" {
		t.Fatalf("worker failed after disconnect: %+v %v", job, err)
	}
}
