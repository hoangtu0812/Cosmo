package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"cosmo/backend/internal/knowledge"
)

func createRetrievalKB(t *testing.T, s *Server, owner, mountedWorkspace, visibility, embedding string) string {
	t.Helper()
	id := "kb_" + randomID(18)
	ctx := context.Background()
	if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_bases(id,name,owner_workspace_id,visibility,version,embedding_model,rerank_enabled) VALUES($1,'Policies',$2,$3,1,$4,false)`, id, owner, visibility, embedding); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.db.Exec(ctx, `DELETE FROM knowledge_bases WHERE id=$1`, id) })
	if mountedWorkspace != "" {
		if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_mounts(kb_id,target_type,target_id) VALUES($1,'workspace',$2)`, id, mountedWorkspace); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestMultiKBRetrievalKeepsOwnerProfilesAndEnforcesAccessBeforeSearchAndLog(t *testing.T) {
	s, first, _, _ := agentAccessFixture(t)
	_, second, _, _ := agentAccessFixture(t)
	ctx := context.Background()
	if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,'https://gateway-a.invalid','a'),($2,'https://gateway-b.invalid','b')`, first.WorkspaceID, second.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	a := createRetrievalKB(t, s, first.WorkspaceID, first.WorkspaceID, "workspace", "embed-a")
	b := createRetrievalKB(t, s, second.WorkspaceID, first.WorkspaceID, "everyone", "embed-b")
	private := createRetrievalKB(t, s, second.WorkspaceID, first.WorkspaceID, "workspace", "embed-private")
	unmounted := createRetrievalKB(t, s, first.WorkspaceID, "", "workspace", "embed-unmounted")
	var mu sync.Mutex
	var calls []string
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			KBIDs     []string `json:"kb_ids"`
			Embedding string   `json:"embedding_model"`
			Limit     int      `json:"limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.KBIDs) != 1 {
			t.Error("invalid search request")
			w.WriteHeader(400)
			return
		}
		id := input.KBIDs[0]
		mu.Lock()
		calls = append(calls, id)
		mu.Unlock()
		wantModel, wantGateway := "embed-a", "https://gateway-a.invalid"
		if id == b {
			wantModel, wantGateway = "embed-b", "https://gateway-b.invalid"
		}
		if id != a && id != b {
			t.Errorf("searched disallowed KB: %s", id)
		}
		if input.Embedding != wantModel || r.Header.Get("X-Cosmo-Gateway-Base-URL") != wantGateway || input.Limit > 2 {
			t.Errorf("wrong profile/budget: %+v %s", input, r.Header.Get("X-Cosmo-Gateway-Base-URL"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []knowledge.Passage{{KBID: private, DocumentID: "secret-document", Text: "SECRET"}, {KBID: id, DocumentID: "doc-" + id, Text: "Allowed evidence", Score: 1}}})
	}))
	defer rag.Close()
	s.knowledge = knowledge.New(rag.URL, time.Second)
	s.cfg.RetrievalLog = true
	s.cfg.RetrievalCandidates = 2
	query := "test-" + randomID(12)
	report, err := s.retrieveKnowledge(ctx, first.WorkspaceID, query, []string{a, b, private, unmounted})
	if err != nil || len(report.Passages) != 2 || len(report.Sources) != 2 || !report.incomplete() {
		t.Fatalf("unexpected report: %+v %v", report, err)
	}
	var logged string
	if err := s.db.QueryRow(ctx, `SELECT passages::text FROM knowledge_retrieval_log WHERE workspace_id=$1 AND query=$2`, first.WorkspaceID, query).Scan(&logged); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logged, "secret-document") || strings.Contains(logged, private) || !strings.Contains(logged, "fusion_score") {
		t.Fatalf("unsafe or incomplete retrieval log: %s", logged)
	}
	// Revoking a share prevents outbound search on the very next retrieval,
	// even though the mount and the agent's reading list still name the KB.
	if _, err := s.db.Exec(ctx, `UPDATE knowledge_bases SET visibility='workspace' WHERE id=$1`, b); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	calls = nil
	mu.Unlock()
	report, err = s.retrieveKnowledge(ctx, first.WorkspaceID, query, []string{b, private, unmounted})
	if err != nil || len(report.Passages) != 0 || len(report.Sources) != 0 {
		t.Fatalf("revoked sources exposed: %+v %v", report, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("called revoked/unmounted KBs: %v", calls)
	}
}
