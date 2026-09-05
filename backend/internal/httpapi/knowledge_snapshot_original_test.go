package httpapi

import (
	"context"
	"cosmo/backend/internal/knowledge"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSnapshotOriginalSurvivesDocumentDeletionAndChecksCurrentAccess(t *testing.T) {
	s, agent, _, _ := agentAccessFixture(t)
	_, readerAgent, reader, _ := agentAccessFixture(t)
	ctx := context.Background()
	kb := createRetrievalKB(t, s, agent.WorkspaceID, readerAgent.WorkspaceID, "everyone", "embed")
	id := "kbs_" + randomID(18)
	// No live document exists; the original is resolved exclusively from the snapshot.
	original, _ := json.Marshal(map[string]knowledge.SnapshotOriginal{"deleted-doc": {StorageKey: "snapshot-file", Filename: "original.txt", ContentType: "text/plain", SizeBytes: 3}})
	if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_snapshots(id,kb_id,version,manifest,model_settings,chunks,digest,originals) VALUES($1,$2,1,'{}','{}',1,'test',$3)`, id, kb, string(original)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE users SET last_workspace_id=$2 WHERE id=$1`, reader.ID, readerAgent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("storage_key") != "snapshot-file" {
			t.Error("wrong original")
		}
		fmt.Fprint(w, "old")
	}))
	defer rag.Close()
	s.knowledge = knowledge.New(rag.URL, time.Second)
	router := chi.NewRouter()
	router.Get("/knowledge/{kbID}/documents/{documentID}/original", s.openKnowledgeDocumentOriginal)
	read := func(pin string, want int) {
		t.Helper()
		r := httptest.NewRequest("GET", "/knowledge/"+kb+"/documents/deleted-doc/original?snapshot_id="+pin, nil)
		r = r.WithContext(context.WithValue(ctx, userContextKey, reader))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("read: %d %s", w.Code, w.Body.String())
		}
		if want == 200 && w.Body.String() != "old" {
			t.Fatal("wrong version")
		}
	}
	read(id, 200)
	read("missing", 404)
	read("", 404)
	if _, err := s.db.Exec(ctx, `UPDATE knowledge_bases SET visibility='workspace' WHERE id=$1`, kb); err != nil {
		t.Fatal(err)
	}
	read(id, 404)
}
