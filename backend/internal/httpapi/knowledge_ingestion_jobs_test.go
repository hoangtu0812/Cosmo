package httpapi

import (
	"bytes"
	"context"
	"cosmo/backend/internal/knowledge"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func ingestionFixture(t *testing.T) (*Server, string, User, []string) {
	t.Helper()
	s, agent, owner, _ := agentAccessFixture(t)
	ctx := context.Background()
	if _, err := s.db.Exec(ctx, `UPDATE users SET last_workspace_id=$2 WHERE id=$1`, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,'https://gateway.invalid','test')`, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	kb := createRetrievalKB(t, s, agent.WorkspaceID, agent.WorkspaceID, "workspace", "embed")
	docs := []string{"doc_" + randomID(16), "doc_" + randomID(16)}
	for _, id := range docs {
		if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_documents(id,kb_id,title,filename,content_type,size_bytes,storage_key,status,chunk_count,uploaded_by) VALUES($1,$2,'test','test.txt','text/plain',4,'original','ready',2,$3)`, id, kb, owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	s.cfg.RAGTimeout = 5 * time.Second
	return s, kb, owner, docs
}

func queueIngestionTest(t *testing.T, s *Server, kb string, user User) string {
	t.Helper()
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, `SELECT id FROM knowledge_bases WHERE id=$1 FOR UPDATE`, kb).Scan(&id); err != nil {
		t.Fatal(err)
	}
	id, err = enqueueIngestion(ctx, tx, kb, user.ID, "queued")
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestIngestionRecoveredAttemptPublishesOnceAndKeepsOldIndexUntilComplete(t *testing.T) {
	s, kb, owner, docs := ingestionFixture(t)
	ctx := context.Background()
	configured, err := s.configuredKnowledgeModelSettings(ctx, kb)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := json.Marshal(configured)
	old := "kbs_" + strings.Repeat("a", 32)
	if _, err = s.db.Exec(ctx, `UPDATE knowledge_bases SET live_index_id=$2,live_index_settings=$3 WHERE id=$1`, kb, old, string(saved)); err != nil {
		t.Fatal(err)
	}
	id := queueIngestionTest(t, s, kb, owner)
	abandoned, err := s.claimIngestionJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(ctx, `UPDATE knowledge_ingestion_jobs SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = s.recoverInterruptedIngestions(ctx); err != nil {
		t.Fatal(err)
	}
	var processing int
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_documents WHERE kb_id=$1 AND status='processing'`, kb).Scan(&processing)
	if processing != 2 {
		t.Fatal("restart discarded durable work")
	}
	fresh, err := s.claimIngestionJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID != id || fresh.Attempts != 2 || fresh.AttemptID == abandoned.AttemptID {
		t.Fatal("attempt not fenced")
	}
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body knowledge.IngestRequest
		json.NewDecoder(r.Body).Decode(&body)
		if body.TargetSnapshotID == "" || body.DeadlineEpoch <= 0 || body.ContentBase64 != "" || body.StorageKey != "original" {
			t.Error("worker lost durable source/attempt")
		}
		var current string
		s.db.QueryRow(ctx, `SELECT live_index_id FROM knowledge_bases WHERE id=$1`, kb).Scan(&current)
		if body.TargetSnapshotID == fresh.AttemptID && current != old {
			t.Error("index switched before all documents finished")
		}
		fmt.Fprintln(w, `{"stage":"done","chunks":3,"storage_key":"original"}`)
	}))
	defer rag.Close()
	s.knowledge = knowledge.New(rag.URL, time.Second)
	if err = s.executeIngestionJob(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err = s.executeIngestionJob(ctx, abandoned); err != nil {
		t.Fatal(err)
	}
	var index, status string
	var done int
	s.db.QueryRow(ctx, `SELECT live_index_id FROM knowledge_bases WHERE id=$1`, kb).Scan(&index)
	s.db.QueryRow(ctx, `SELECT status FROM knowledge_ingestion_jobs WHERE id=$1`, id).Scan(&status)
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_document_events WHERE document_id=ANY($1) AND stage='done'`, docs).Scan(&done)
	if index != fresh.AttemptID || status != "succeeded" || done != 2 {
		t.Fatalf("bad publication %s %s %d", index, status, done)
	}
	var cleanup int
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_snapshot_cleanup WHERE snapshot_id=ANY($1)`, []string{old, abandoned.AttemptID}).Scan(&cleanup)
	if cleanup != 2 {
		t.Fatal("old generations not queued for cleanup")
	}
	// New configuration must not make the previous generation unreadable while
	// the next rebuild is being prepared (credentials remain current).
	s.db.Exec(ctx, `UPDATE knowledge_bases SET embedding_model='next-model',updated_at=NOW() WHERE id=$1`, kb)
	read, err := s.knowledgeModelSettingsForKB(ctx, kb)
	if err != nil || read.EmbeddingModel != "embed" || read.LiveIndexID != fresh.AttemptID {
		t.Fatalf("live profile drift: %v %v", read.EmbeddingModel, err)
	}
}

func TestIngestionFailureConfigChangeAndPermissionDoNotPublish(t *testing.T) {
	for _, scenario := range []string{"remote_failure", "config_change", "permission_revoked", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			s, kb, owner, _ := ingestionFixture(t)
			ctx := context.Background()
			id := queueIngestionTest(t, s, kb, owner)
			job, err := s.claimIngestionJob(ctx)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch scenario {
				case "remote_failure":
					if calls == 2 {
						w.WriteHeader(503)
						return
					}
				case "config_change":
					s.db.Exec(ctx, `UPDATE knowledge_bases SET updated_at=NOW() WHERE id=$1`, kb)
				case "permission_revoked":
					s.db.Exec(ctx, `DELETE FROM workspace_memberships WHERE user_id=$1`, owner.ID)
				case "expired":
					s.db.Exec(ctx, `UPDATE knowledge_ingestion_jobs SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, id)
				}
				fmt.Fprintln(w, `{"stage":"done","chunks":3,"storage_key":"original"}`)
			}))
			defer rag.Close()
			s.knowledge = knowledge.New(rag.URL, time.Second)
			if err = s.executeIngestionJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			var index, status string
			var done int
			s.db.QueryRow(ctx, `SELECT live_index_id FROM knowledge_bases WHERE id=$1`, kb).Scan(&index)
			s.db.QueryRow(ctx, `SELECT status FROM knowledge_ingestion_jobs WHERE id=$1`, id).Scan(&status)
			s.db.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_document_events e JOIN knowledge_documents d ON d.id=e.document_id WHERE d.kb_id=$1 AND e.stage='done'`, kb).Scan(&done)
			if index != "" || status == "succeeded" || done != 0 {
				t.Fatal("unfinished generation published")
			}
			if scenario == "config_change" || scenario == "permission_revoked" {
				if status != "failed" {
					t.Fatal("terminal change retried")
				}
			}
		})
	}
}

func TestUploadPersistsIntentAndOriginalBeforeWorker(t *testing.T) {
	s, kb, owner, _ := ingestionFixture(t)
	ctx := context.Background()
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/originals" {
			t.Error("ingest ran in HTTP handler")
			w.WriteHeader(500)
			return
		}
		var body struct {
			DocumentID string `json:"document_id"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		var exists bool
		s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_documents d JOIN knowledge_ingestion_jobs j ON j.kb_id=d.kb_id WHERE d.id=$1 AND d.storage_key='knowledge-uploads/' || d.id AND j.status='uploading')`, body.DocumentID).Scan(&exists)
		if !exists {
			t.Error("storage called before durable intent")
		}
		json.NewEncoder(w).Encode(map[string]any{"storage_key": "knowledge-uploads/" + body.DocumentID, "size_bytes": 4})
	}))
	defer rag.Close()
	s.knowledge = knowledge.New(rag.URL, time.Second)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, _ := form.CreateFormFile("file", "test.txt")
	file.Write([]byte("test"))
	form.Close()
	r := httptest.NewRequest("POST", "/knowledge/"+kb+"/documents", &body).WithContext(context.WithValue(ctx, userContextKey, owner))
	r.Header.Set("Content-Type", form.FormDataContentType())
	router := chi.NewRouter()
	router.Post("/knowledge/{kbID}/documents", s.uploadKnowledgeDocument)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var queued int
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_ingestion_jobs WHERE kb_id=$1 AND status='queued'`, kb).Scan(&queued)
	if queued != 1 {
		t.Fatal("upload not durably queued")
	}
}

func TestIngestionRetryRestoresOnlyCompletedDocument(t *testing.T) {
	s, kb, owner, _ := ingestionFixture(t)
	ctx := context.Background()
	id := queueIngestionTest(t, s, kb, owner)
	first, err := s.claimIngestionJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	restored := ""
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body knowledge.IngestRequest
		json.NewDecoder(r.Body).Decode(&body)
		calls++
		if calls == 2 {
			w.WriteHeader(503)
			return
		}
		if calls == 3 {
			if body.CheckpointSnapshotID != first.AttemptID || body.CheckpointChunks != 3 {
				t.Error("completed checkpoint missing")
			}
			restored = body.DocumentID
		}
		if calls == 4 && body.CheckpointSnapshotID != "" {
			t.Error("incomplete document checkpointed")
		}
		fmt.Fprintln(w, `{"stage":"done","chunks":3,"storage_key":"original"}`)
	}))
	defer rag.Close()
	s.knowledge = knowledge.New(rag.URL, time.Second)
	if err = s.executeIngestionJob(ctx, first); err != nil {
		t.Fatal(err)
	}
	var count int
	s.db.QueryRow(ctx, `SELECT count(*) FROM knowledge_ingestion_checkpoints WHERE job_id=$1`, id).Scan(&count)
	if count != 1 {
		t.Fatal("must checkpoint only completed document", count)
	}
	s.db.Exec(ctx, `UPDATE knowledge_ingestion_jobs SET next_attempt_at=NOW() WHERE id=$1`, id)
	second, err := s.claimIngestionJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.AttemptID == first.AttemptID {
		t.Fatal("attempt reused")
	}
	if err = s.executeIngestionJob(ctx, second); err != nil {
		t.Fatal(err)
	}
	if restored == "" || calls != 4 {
		t.Fatal("restore was not exercised")
	}
	var status string
	s.db.QueryRow(ctx, `SELECT status FROM knowledge_ingestion_jobs WHERE id=$1`, id).Scan(&status)
	if status != "succeeded" {
		t.Fatal(status)
	}
}
