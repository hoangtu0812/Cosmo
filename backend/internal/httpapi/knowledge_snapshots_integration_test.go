package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cosmo/backend/internal/agents"
	"cosmo/backend/internal/knowledge"
	"cosmo/backend/internal/runs"
	"cosmo/backend/internal/tools"
	"github.com/go-chi/chi/v5"
)

func TestKnowledgeSnapshotPublicationAndPinnedRuntime(t *testing.T) {
	s, agent, owner, member := agentAccessFixture(t)
	ctx := context.Background()
	kb := createRetrievalKB(t, s, agent.WorkspaceID, agent.WorkspaceID, "workspace", "original-model")
	doc := "doc_" + randomID(12)
	for _, statement := range []string{
		`INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,'https://gateway.invalid','chat')`,
	} {
		if _, err := s.db.Exec(ctx, statement, agent.WorkspaceID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_documents(id,kb_id,title,filename,content_type,size_bytes,storage_key,status,chunk_count,uploaded_by) VALUES($1,$2,'Test','test.txt','text/plain',10,'original','ready',1,$3)`, doc, kb, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO agent_knowledge_bases(agent_id,kb_id) VALUES($1,$2)`, agent.ID, kb); err != nil {
		t.Fatal(err)
	}
	if _, err := s.agents.PublishKnowledge(ctx, agent.ID, owner.ID, "", "snapshot"); !errors.Is(err, agents.ErrKnowledgeSnapshotRequired) {
		t.Fatalf("legacy KB silently pinned: %v", err)
	}
	changeDuringCopy := false
	revokeDuringSearch := false
	deleted := 0
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted++
			w.WriteHeader(204)
			return
		}
		var body struct {
			ID        string                                `json:"snapshot_id"`
			KB        string                                `json:"kb_id"`
			Documents map[string]int                        `json:"documents"`
			Embedding string                                `json:"embedding_model"`
			Originals map[string]knowledge.SnapshotOriginal `json:"originals"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == "/snapshots" {
			if body.KB != kb || body.Documents[doc] != 1 {
				t.Errorf("incorrect manifest: %+v", body)
			}
			if changeDuringCopy {
				if _, err := s.db.Exec(ctx, `UPDATE knowledge_documents SET updated_at=NOW(),version=version+1 WHERE id=$1`, doc); err != nil {
					t.Error(err)
				}
			}
			for key, original := range body.Originals {
				original.StorageKey = "knowledge-snapshots/" + body.ID + "/" + key
				body.Originals[key] = original
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"snapshot_id": body.ID, "chunks": 1, "digest": strings.Repeat("a", 64), "originals": body.Originals})
			return
		}
		if body.ID == "" || body.Embedding != "original-model" {
			t.Errorf("retrieval lost snapshot profile: %+v", body)
		}
		if revokeDuringSearch {
			if _, err := s.db.Exec(ctx, `DELETE FROM knowledge_mounts WHERE kb_id=$1`, kb); err != nil {
				t.Error(err)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []knowledge.Passage{{KBID: kb, DocumentID: doc, Text: "Frozen evidence"}}})
	}))
	defer rag.Close()
	s.knowledge = knowledge.New(rag.URL, time.Second)
	id, version, err := s.publishKnowledgeSnapshot(ctx, kb)
	if err != nil || version != 2 {
		t.Fatalf("publish: %s %d %v", id, version, err)
	}
	release, err := s.agents.PublishKnowledge(ctx, agent.ID, owner.ID, "snapshot release", "snapshot")
	if err != nil || release.KnowledgeSnapshots[kb] != id {
		t.Fatalf("release: %+v %v", release, err)
	}
	changeDuringCopy = true
	if _, _, err := s.publishKnowledgeSnapshot(ctx, kb); !errors.Is(err, errSnapshotChanged) {
		t.Fatalf("accepted changing manifest: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("failed copy not cleaned: %d", deleted)
	}
	var published int
	if err := s.db.QueryRow(ctx, `SELECT version FROM knowledge_bases WHERE id=$1`, kb).Scan(&published); err != nil || published != version {
		t.Fatalf("failed publish moved pointer: %d %v", published, err)
	}
	changeDuringCopy = false
	if _, _, err := s.publishKnowledgeSnapshot(ctx, kb); err != nil {
		t.Fatal(err)
	}
	runtime, err := s.agentRuntime(ctx, member, agent.WorkspaceID, agent.ID, release.ID)
	if err != nil || runtime.KnowledgeSnapshots[kb] != id {
		t.Fatalf("old release moved: %+v %v", runtime, err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE knowledge_bases SET embedding_model='new-model' WHERE id=$1`, kb); err != nil {
		t.Fatal(err)
	}
	report, err := s.retrieveKnowledgePinned(ctx, agent.WorkspaceID, "evidence", []string{kb}, runtime.KnowledgeSnapshots)
	if err != nil || len(report.Passages) != 1 || report.Sources[0].SnapshotID != id {
		t.Fatalf("pinned retrieval: %+v %v", report, err)
	}
	// An ID belonging to another KB cannot be substituted, even with a valid mount.
	report, err = s.retrieveKnowledgePinned(ctx, agent.WorkspaceID, "evidence", []string{kb}, map[string]string{kb: "kbs_" + strings.Repeat("0", 32)})
	if err != nil || len(report.Passages) != 0 || !report.incomplete() {
		t.Fatalf("invalid pin accepted: %+v %v", report, err)
	}
	revokeDuringSearch = true
	report, err = s.retrieveKnowledgePinned(ctx, agent.WorkspaceID, "evidence", []string{kb}, runtime.KnowledgeSnapshots)
	if err != nil || len(report.Passages) != 0 || !report.incomplete() {
		t.Fatalf("revoked evidence leaked: %+v %v", report, err)
	}
	if _, err := s.agentRuntime(ctx, member, agent.WorkspaceID, agent.ID, release.ID); !errors.Is(err, agents.ErrKnowledgeSnapshotRequired) {
		t.Fatalf("revoked runtime accepted: %v", err)
	}
}

func TestSnapshotSettingsExcludeCredentials(t *testing.T) {
	raw, err := json.Marshal(knowledge.ModelSettings{GatewayAPIKey: "never-store-this", SnapshotID: "not-settings", EmbeddingModel: "embed"})
	if err != nil || strings.Contains(string(raw), "never-store-this") || strings.Contains(string(raw), "not-settings") {
		t.Fatalf("unsafe settings: %s %v", raw, err)
	}
}

func TestChatWorkerUsesPinnedKnowledgeSnapshot(t *testing.T) {
	testChatPinnedKnowledge(t, false)
}

func TestWorkspaceChatWorkerUsesPinnedKnowledgeSnapshot(t *testing.T) {
	testChatPinnedKnowledge(t, true)
}

func testChatPinnedKnowledge(t *testing.T, workspacePin bool) {
	s, agent, owner, _ := agentAccessFixture(t)
	ctx := context.Background()
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/model/info" {
			w.WriteHeader(404)
			return
		}
		content := "CO"
		if calls.Add(1) == 2 {
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "Frozen evidence") {
				t.Errorf("generation missing snapshot evidence: %s", body)
			}
			content = "Frozen answer [1]."
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\ndata: [DONE]\n\n", content)
	}))
	defer gateway.Close()
	kb := createRetrievalKB(t, s, agent.WorkspaceID, agent.WorkspaceID, "workspace", "embed")
	id := "kbs_" + strings.Repeat("b", 32)
	settings, _ := json.Marshal(knowledge.ModelSettings{EmbeddingScope: agent.WorkspaceID, EmbeddingModel: "embed", GatewayBaseURL: gateway.URL})
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,$2,'test-model')`, []any{agent.WorkspaceID, gateway.URL}},
		{`INSERT INTO knowledge_snapshots(id,kb_id,version,manifest,model_settings,chunks,digest) VALUES($1,$2,1,'{}',$3,1,$4)`, []any{id, kb, string(settings), strings.Repeat("a", 64)}},
		{`INSERT INTO agent_knowledge_bases(agent_id,kb_id) VALUES($1,$2)`, []any{agent.ID, kb}},
		{`UPDATE agents SET model='test-model',has_suggested_questions=false WHERE id=$1`, []any{agent.ID}},
	} {
		if _, err := s.db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	release, err := s.agents.PublishKnowledge(ctx, agent.ID, owner.ID, "", "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			SnapshotID string `json:"snapshot_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.SnapshotID != id {
			t.Errorf("worker lost pin: %+v %v", input, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []knowledge.Passage{{KBID: kb, DocumentID: "doc", DocumentTitle: "Policy", Text: "Frozen evidence"}}})
	}))
	defer rag.Close()
	s.knowledge = knowledge.New(rag.URL, time.Second)
	s.runs = runs.NewRepository(s.db)
	s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{}, tools.SearchBackend{})
	s.cfg.LLMRequestTimeout = time.Second
	conversation := "con_" + randomID(18)
	agentID, releaseID := agent.ID, release.ID
	if workspacePin {
		agentID, releaseID = "", ""
		if _, err := s.db.Exec(ctx, `UPDATE knowledge_mounts SET snapshot_id=$2 WHERE kb_id=$1`, kb, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title,agent_id,agent_version_id) VALUES($1,$2,$3,'Test',NULLIF($4,''),NULLIF($5,''))`, conversation, owner.ID, agent.WorkspaceID, agentID, releaseID); err != nil {
		t.Fatal(err)
	}
	startTestChatWorkers(t, s)
	router := chi.NewRouter()
	router.Post("/conversations/{conversationID}/messages", s.chat)
	r := httptest.NewRequest(http.MethodPost, "/conversations/"+conversation+"/messages", strings.NewReader(`{"content":"Quy định nội bộ là gì?"}`))
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, owner))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event: done") || !strings.Contains(w.Body.String(), "Frozen answer") {
		t.Fatalf("chat failed: %d %s", w.Code, w.Body.String())
	}
	var input, output string
	if err := s.db.QueryRow(ctx, `SELECT runs.input::text,steps.output::text FROM runs JOIN run_steps steps ON steps.run_id=runs.id WHERE runs.input->>'conversation_id'=$1 AND steps.node_id='retrieval'`, conversation).Scan(&input, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(input, id) || !strings.Contains(output, id) {
		t.Fatalf("run lost snapshot provenance: %s %s", input, output)
	}
}
