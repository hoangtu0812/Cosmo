package httpapi

import (
	"context"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkspaceMountSnapshotUpdatesAreExplicit(t *testing.T) {
	s, agent, owner, member := agentAccessFixture(t)
	ctx := context.Background()
	kb := createRetrievalKB(t, s, agent.WorkspaceID, agent.WorkspaceID, "workspace", "embed")
	router := chi.NewRouter()
	router.Put("/workspaces/{workspaceID}/knowledge/{kbID}", s.mountKnowledge)
	request := func(user User, body string, want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPut, "/workspaces/"+agent.WorkspaceID+"/knowledge/"+kb, strings.NewReader(body))
		r = r.WithContext(context.WithValue(ctx, userContextKey, user))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("mount %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	request(member, `{"knowledge_mode":"snapshot"}`, 403)
	request(owner, `{"knowledge_mode":"snapshot"}`, 409)
	request(owner, `{"knowledge_mode":"unknown"}`, 400)
	first := "kbs_" + randomID(12)
	second := "kbs_" + randomID(12)
	for version, id := range []string{first, second} {
		if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_snapshots(id,kb_id,version,manifest,model_settings,chunks,digest) VALUES($1,$2,$3,'{}','{}',1,'digest')`, id, kb, version+1); err != nil {
			t.Fatal(err)
		}
	}
	request(owner, `{"knowledge_mode":"snapshot"}`, 204)
	if _, err := s.db.Exec(ctx, `UPDATE knowledge_bases SET version=2 WHERE id=$1`, kb); err != nil {
		t.Fatal(err)
	}
	pins, err := s.workspaceKnowledgePins(ctx, agent.WorkspaceID)
	if err != nil || pins[kb] != first {
		t.Fatalf("publication moved mount: %v %v", pins, err)
	}
	request(owner, `{}`, 204)
	pins, err = s.workspaceKnowledgePins(ctx, agent.WorkspaceID)
	if err != nil || pins[kb] != second {
		t.Fatalf("update lost snapshot mode: %v %v", pins, err)
	}
	request(owner, `{"knowledge_mode":"live"}`, 204)
	pins, err = s.workspaceKnowledgePins(ctx, agent.WorkspaceID)
	if err != nil || pins[kb] != "" {
		t.Fatalf("live switch failed: %v %v", pins, err)
	}
}
