package httpapi

import (
	"context"
	"cosmo/backend/internal/knowledge"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func settingsRequest(s *Server, kb string, user User, method, suffix, body string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Patch("/knowledge/{kbID}", s.updateKnowledgeBase)
	router.Post("/knowledge/{kbID}/reindex", s.reindexKnowledgeBase)
	router.Get("/knowledge/{kbID}/capabilities", s.knowledgeCapabilities)
	r := httptest.NewRequest(method, "/knowledge/"+kb+suffix, strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, user))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestKnowledgeSettingsLiveControlsPreserveIndexAndSnapshot(t *testing.T) {
	s, kb, owner, _ := ingestionFixture(t)
	ctx := context.Background()
	old, err := s.configuredKnowledgeModelSettings(ctx, kb)
	if err != nil {
		t.Fatal(err)
	}
	old.RerankEnabled = false
	old.TopK = 8
	raw, _ := json.Marshal(old)
	snapshot := "kbs_" + strings.Repeat("a", 32)
	if _, err = s.db.Exec(ctx, `UPDATE knowledge_bases SET live_index_id=$2,live_index_settings=$3,rerank_enabled=false WHERE id=$1`, kb, snapshot, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(ctx, `INSERT INTO knowledge_snapshots(id,kb_id,version,manifest,model_settings,chunks,digest) VALUES($1,$2,1,'{}',$3,4,$4)`, snapshot, kb, string(raw), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	w := settingsRequest(s, kb, owner, "PATCH", "", `{"retrieval_mode":"keyword","retrieval_top_k":3,"score_threshold":0.7,"rerank_enabled":true,"reranker_model":"rerank-new"}`)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		KnowledgeBase KnowledgeBase `json:"knowledge_base"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response.KnowledgeBase.NeedsReindex {
		t.Fatal("query changes incorrectly require re-index")
	}
	live, err := s.knowledgeModelSettingsForKB(ctx, kb)
	if err != nil {
		t.Fatal(err)
	}
	if live.TopK != 3 || live.RetrievalMode != "keyword" || !live.RerankEnabled || live.RerankerModel != "rerank-new" || live.ScoreThreshold != 0.7 {
		t.Fatalf("query settings ignored: %+v", live)
	}
	pinned, err := s.snapshotModelSettings(ctx, kb, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.TopK != 8 || pinned.RerankEnabled {
		t.Fatal("published settings mutated")
	}
	w = settingsRequest(s, kb, owner, "PATCH", "", `{"embedding_model":"embed-new","chunk_size":1200}`)
	if w.Code != 200 {
		t.Fatalf("index settings: %d %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	if !response.KnowledgeBase.NeedsReindex {
		t.Fatal("index changes not marked stale")
	}
	live, err = s.knowledgeModelSettingsForKB(ctx, kb)
	if err != nil {
		t.Fatal(err)
	}
	if live.EmbeddingModel != old.EmbeddingModel || live.ChunkSize != old.ChunkSize {
		t.Fatal("vector provenance changed before re-index")
	}
}

func TestKnowledgeSettingsRollbackAndScopedReindex(t *testing.T) {
	s, kb, owner, _ := ingestionFixture(t)
	ctx := context.Background()
	var original string
	s.db.QueryRow(ctx, `SELECT name FROM knowledge_bases WHERE id=$1`, kb).Scan(&original)
	w := settingsRequest(s, kb, owner, "PATCH", "", `{"name":"must rollback","chunk_size":4096,"chunk_overlap":2049}`)
	if w.Code != 400 {
		t.Fatalf("overlap validation: %d %s", w.Code, w.Body.String())
	}
	var after string
	s.db.QueryRow(ctx, `SELECT name FROM knowledge_bases WHERE id=$1`, kb).Scan(&after)
	if after != original {
		t.Fatal("partially saved invalid settings")
	}
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"layout_available":false}`))
	}))
	defer rag.Close()
	s.knowledge = knowledge.New(rag.URL, time.Second)
	w = settingsRequest(s, kb, owner, "PATCH", "", `{"layout_mode":"always"}`)
	if w.Code != 400 {
		t.Fatal("unconfigured layout accepted")
	}
	w = settingsRequest(s, kb, owner, "PATCH", "", `{"layout_mode":"off","rerank_enabled":false,"chunk_size":1024,"reindex":true}`)
	if w.Code != 200 {
		t.Fatalf("atomic save/reindex: %d %s", w.Code, w.Body.String())
	}
	job, err := s.claimIngestionJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job.KBID != kb || job.RequestedBy != owner.ID {
		t.Fatal("incorrect re-index owner or scope")
	}
	var manifest ingestionManifestData
	json.Unmarshal([]byte(job.Manifest), &manifest)
	if manifest.Layout != "off" || len(manifest.Documents) != 2 {
		t.Fatal("admitted stale configuration")
	}
	w = settingsRequest(s, kb, owner, "POST", "/reindex", ``)
	if w.Code != 409 {
		t.Fatal("duplicate re-index accepted")
	}
	w = settingsRequest(s, kb, owner, "PATCH", "", `{"chunk_size":2048}`)
	if w.Code != 409 {
		t.Fatal("settings changed during indexing")
	}
	w = settingsRequest(s, kb, User{ID: "missing"}, "POST", "/reindex", ``)
	if w.Code != 403 {
		t.Fatal("unauthorized re-index accepted")
	}
}
