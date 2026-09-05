package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"cosmo/backend/internal/knowledge"
)

func TestReindexRetainsIndexAndAtomicallyAdmitsDocuments(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	owner.Role = "admin"
	ctx := context.Background()
	kb := createRetrievalKB(t, s, agent.WorkspaceID, "", "workspace", "embed")
	if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,'https://gateway.invalid','test')`, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	docs := []string{"doc_" + randomID(16), "doc_" + randomID(16)}
	for i, doc := range docs {
		if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_documents(id,kb_id,title,filename,content_type,size_bytes,storage_key,status,chunk_count,uploaded_by,created_at) VALUES($1,$2,'Test','test.txt','text/plain',10,'original','ready',7,$3,NOW()+$4*INTERVAL '1 second')`, doc, kb, owner.ID, i); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	started, release := make(chan struct{}, 2), make(chan struct{})
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/ingest" {
			t.Errorf("unexpected RAG request: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		if r.Header.Get("X-Cosmo-Embedding-Scope") != agent.WorkspaceID {
			t.Error("ingestion lost profile owner")
		}
		started <- struct{}{}
		<-release
		fmt.Fprintln(w, `{"stage":"done","chunks":9,"storage_key":"original"}`)
	}))
	defer rag.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	s.knowledge = knowledge.New(rag.URL, 5*time.Second)
	s.cfg.RAGTimeout = 5 * time.Second
	s.cfg.ReindexWorkers = 1
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/reindex", nil).WithContext(context.WithValue(ctx, userContextKey, owner))
		w := httptest.NewRecorder()
		s.reindexKnowledgeDocuments(w, r)
		return w
	}
	// Fail on the second document after updating the first: everything rolls back.
	constraint := "test_reindex_" + randomID(8)
	if _, err := s.db.Exec(ctx, fmt.Sprintf(`ALTER TABLE knowledge_document_events ADD CONSTRAINT %s CHECK(document_id <> '%s' OR stage <> 'reindex') NOT VALID`, constraint, docs[1])); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.db.Exec(ctx, `ALTER TABLE knowledge_document_events DROP CONSTRAINT IF EXISTS `+constraint)
	})
	if w := request(); w.Code != 500 {
		t.Fatalf("preparation: %d %s", w.Code, w.Body.String())
	}
	var ready, events int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_documents WHERE kb_id=$1 AND status='ready' AND chunk_count=7`, kb).Scan(&ready); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_document_events WHERE document_id=ANY($1)`, docs).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if ready != 2 || events != 0 || calls.Load() != 0 {
		t.Fatalf("partial admission: ready=%d events=%d calls=%d", ready, events, calls.Load())
	}
	if _, err := s.db.Exec(ctx, `ALTER TABLE knowledge_document_events DROP CONSTRAINT `+constraint); err != nil {
		t.Fatal(err)
	}
	if w := request(); w.Code != 202 {
		t.Fatalf("admission: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("ingestion did not start")
	}
	if w := request(); w.Code != 409 {
		t.Fatalf("duplicate rebuild admitted: %d", w.Code)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_documents WHERE kb_id=$1 AND chunk_count=7`, kb).Scan(&ready); err != nil || ready != 2 {
		t.Fatalf("old index metadata cleared: %d %v", ready, err)
	}
	close(release)
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_documents WHERE kb_id=$1 AND status='ready' AND chunk_count=9`, kb).Scan(&ready); err != nil {
			t.Fatal(err)
		}
		if ready == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("reindex did not finish")
		case <-ticker.C:
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("reindex made %d calls", calls.Load())
	}
}
