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

func TestRetrievalKnowledgeModeIncludesEmptyAndFailedSources(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sources []knowledgeSourceStatus
		want    string
	}{
		{"none", nil, "live"},
		{"live", []knowledgeSourceStatus{{KBID: "one", Status: "ready"}}, "live"},
		{"empty snapshot", []knowledgeSourceStatus{{KBID: "one", SnapshotID: "pin", Status: "empty"}}, "snapshot"},
		{"mixed failure", []knowledgeSourceStatus{{KBID: "one", Status: "ready"}, {KBID: "two", SnapshotID: "pin", Status: "failed"}}, "mixed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retrievalKnowledgeMode(tc.sources); got != tc.want {
				t.Fatalf("mode %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRetrievalAPIUsesChatFusionAndAccessBoundary(t *testing.T) {
	s, agent, owner, outsider := agentAccessFixture(t)
	ctx := context.Background()
	s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,'https://gateway.invalid','test')`, agent.WorkspaceID)
	first := createRetrievalKB(t, s, agent.WorkspaceID, agent.WorkspaceID, "workspace", "embed")
	second := createRetrievalKB(t, s, agent.WorkspaceID, agent.WorkspaceID, "workspace", "embed")
	hidden := createRetrievalKB(t, s, agent.WorkspaceID, "", "workspace", "embed")
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			KBIDs []string `json:"kb_ids"`
		}
		json.NewDecoder(r.Body).Decode(&input)
		id := input.KBIDs[0]
		if id == hidden {
			t.Error("searched unmounted KB")
		}
		score := .02
		if id == first {
			score = 900
		}
		json.NewEncoder(w).Encode(map[string]any{"results": []knowledge.Passage{{KBID: id, DocumentID: "doc-" + id, Text: "Evidence", Score: score}}})
	}))
	defer rag.Close()
	s.knowledge = knowledge.New(rag.URL, time.Second)
	router := chi.NewRouter()
	router.Post("/workspaces/{workspaceID}/retrieve", s.testWorkspaceRetrieval)
	body, _ := json.Marshal(map[string]any{"query": "Question", "kb_ids": []string{first, second, hidden}})
	request := httptest.NewRequest("POST", "/workspaces/"+agent.WorkspaceID+"/retrieve", strings.NewReader(string(body))).WithContext(context.WithValue(ctx, userContextKey, owner))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || strings.Contains(response.Body.String(), hidden) {
		t.Fatalf("bad retrieval boundary: %d", response.Code)
	}
	var decoded struct {
		Passages []knowledgePassage      `json:"passages"`
		Sources  []knowledgeSourceStatus `json:"sources"`
	}
	json.Unmarshal(response.Body.Bytes(), &decoded)
	direct, err := s.retrieveKnowledge(ctx, agent.WorkspaceID, "Question", []string{first, second, hidden})
	if err != nil || len(decoded.Passages) != 2 || len(decoded.Sources) != 2 || decoded.Passages[0].KBID != direct.Passages[0].KBID {
		t.Fatal("evaluation diverges from chat retrieval")
	}
	settings, err := s.knowledgeModelSettingsForKB(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	settingsJSON, _ := json.Marshal(settings)
	pin := "kbs_" + randomID(16)
	if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_snapshots(id,kb_id,version,manifest,model_settings,chunks,digest) VALUES($1,$2,1,'{}',$3,1,'digest')`, pin, second, string(settingsJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE knowledge_mounts SET snapshot_id=$1 WHERE kb_id=$2 AND target_id=$3`, pin, second, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest("POST", "/workspaces/"+agent.WorkspaceID+"/retrieve", strings.NewReader(string(body))).WithContext(context.WithValue(ctx, userContextKey, owner))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"knowledge_mode":"mixed"`) || !strings.Contains(response.Body.String(), `"snapshot_id":"`+pin+`"`) {
		t.Fatalf("mixed provenance missing: %d %s", response.Code, response.Body.String())
	}
	// Explicitly remove fixture membership before checking denial.
	s.db.Exec(ctx, `DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2`, agent.WorkspaceID, outsider.ID)
	request = httptest.NewRequest("POST", "/workspaces/"+agent.WorkspaceID+"/retrieve", strings.NewReader(string(body))).WithContext(context.WithValue(ctx, userContextKey, outsider))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("nonmember retrieval allowed")
	}
}

func TestRetrievalAPIEmptyWorkspaceReturnsArrays(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	s.knowledge = knowledge.New("http://unused.invalid", time.Second)
	router := chi.NewRouter()
	router.Post("/workspaces/{workspaceID}/retrieve", s.testWorkspaceRetrieval)
	request := httptest.NewRequest("POST", "/workspaces/"+agent.WorkspaceID+"/retrieve", strings.NewReader(`{"query":"No evidence"}`)).WithContext(context.WithValue(context.Background(), userContextKey, owner))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"passages":[]`) || !strings.Contains(response.Body.String(), `"sources":[]`) {
		t.Fatalf("empty retrieval contract: %d %s", response.Code, response.Body.String())
	}
}
