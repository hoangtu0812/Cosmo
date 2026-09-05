package httpapi

import (
	"context"
	"cosmo/backend/internal/knowledge"
	"cosmo/backend/internal/runs"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSnapshotCleanupRetainsReferencesAndRetriesStorageFailure(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	ctx := context.Background()
	kb := createRetrievalKB(t, s, agent.WorkspaceID, agent.WorkspaceID, "workspace", "embed")
	ids := []string{}
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("kbs_%032x", time.Now().UnixNano()+int64(i))
		ids = append(ids, id)
		if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_snapshots(id,kb_id,version,manifest,model_settings,chunks,digest,created_at) VALUES($1,$2,$3,'{}','{}',1,'test',NOW()-INTERVAL '60 days')`, id, kb, i+1); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE knowledge_bases SET version=7 WHERE id=$1`, []any{kb}},
		{`UPDATE knowledge_mounts SET snapshot_id=$2 WHERE kb_id=$1`, []any{kb, ids[1]}},
		{`UPDATE knowledge_snapshots SET created_at=NOW() WHERE id=$1`, []any{ids[5]}},
	} {
		if _, err := s.db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	version, err := s.agents.Publish(ctx, agent.ID, owner.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE agent_versions SET knowledge_snapshots=jsonb_build_object($2::text,$3::text) WHERE id=$1`, version.ID, kb, ids[2]); err != nil {
		t.Fatal(err)
	}
	repo := runs.NewRepository(s.db)
	if _, _, err := repo.Create(ctx, runs.NewRun{WorkspaceID: agent.WorkspaceID, ActorUserID: owner.ID, TriggerType: "manual", ResourceType: "conversation", ResourceID: "cleanup-test", Input: map[string]any{"knowledge_snapshots": map[string]string{kb: ids[3]}}}); err != nil {
		t.Fatal(err)
	}
	conversation := "con_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Test')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO messages(id,conversation_id,role,content,citations) VALUES($1,$2,'assistant','Test',jsonb_build_array(jsonb_build_object('snapshot_id',$3::text)))`, "msg_"+randomID(18), conversation, ids[4]); err != nil {
		t.Fatal(err)
	}
	fail := true
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer rag.Close()
	s.knowledge = knowledge.New(rag.URL, time.Second)
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if err := s.collectKnowledgeSnapshots(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	var count, attempts int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM knowledge_snapshots WHERE kb_id=$1`, kb).Scan(&count); err != nil || count != 6 {
		t.Fatalf("removed referenced snapshot: %d %v", count, err)
	}
	if err := s.db.QueryRow(ctx, `SELECT attempts FROM knowledge_snapshot_cleanup WHERE snapshot_id=$1`, ids[0]).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("lost retry: %d %v", attempts, err)
	}
	fail = false
	if _, err := s.db.Exec(ctx, `UPDATE knowledge_snapshot_cleanup SET next_attempt_at=NOW() WHERE snapshot_id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.collectKnowledgeSnapshots(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM knowledge_snapshot_cleanup WHERE snapshot_id=$1`, ids[0]).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cleanup did not finish: %d %v", count, err)
	}
	// Deleting the KB revokes access and queues storage cleanup despite pins.
	if _, err := s.db.Exec(ctx, `DELETE FROM knowledge_bases WHERE id=$1`, kb); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM knowledge_snapshot_cleanup WHERE snapshot_id=ANY($1)`, ids).Scan(&count); err != nil || count != 6 {
		t.Fatalf("KB deletion lost cleanup: %d %v", count, err)
	}
}
