package agents

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func draftFixture(t *testing.T) (*Repository, Agent) {
	t.Helper()
	url := os.Getenv("COSMO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set COSMO_TEST_DATABASE_URL to test atomic draft saves")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	user, workspace := newID("usr_"), newID("wsp_")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,name) VALUES($1,$2,'Draft test')`, user, user+"@test.invalid"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user) })
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces(id,name,slug,type) VALUES($1,'Draft test',$1,'personal')`, workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspace) })
	repo := NewRepository(pool, slog.Default())
	id, err := repo.Create(ctx, NewAgent{Name: "Original", WorkspaceID: workspace, OwnerUserID: user})
	if err != nil {
		t.Fatal(err)
	}
	current, err := repo.Get(ctx, id, user, workspace)
	if err != nil {
		t.Fatal(err)
	}
	return repo, current
}

func TestDraftConcurrentWritersDoNotOverwrite(t *testing.T) {
	repo, current := draftFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	type outcome struct {
		name string
		err  error
	}
	results := make(chan outcome, 2)
	for _, name := range []string{"Writer A", "Writer B"} {
		go func() {
			<-start
			results <- outcome{name, repo.SaveDraft(ctx, current, Changes{Name: &name}, current.DraftRevision)}
		}()
	}
	close(start)
	winner, conflicts := "", 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err == nil {
			winner = result.name
		} else if errors.Is(result.err, ErrStaleDraft) {
			conflicts++
		} else {
			t.Fatal(result.err)
		}
	}
	saved, err := repo.Get(ctx, current.ID, current.OwnerUserID, current.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if conflicts != 1 || saved.Name != winner || saved.DraftRevision != current.DraftRevision+1 {
		t.Fatalf("losing writer changed draft: winner=%q conflicts=%d saved=%#v", winner, conflicts, saved)
	}
}

func TestDraftInvalidKnowledgeRollsBackFieldsAndBindings(t *testing.T) {
	repo, current := draftFixture(t)
	ctx := context.Background()
	kb := newID("kb_")
	if _, err := repo.db.Exec(ctx, `INSERT INTO knowledge_bases(id,name,owner_workspace_id) VALUES($1,'Test KB',$2)`, kb, current.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = repo.db.Exec(ctx, `DELETE FROM knowledge_bases WHERE id=$1`, kb) })
	if _, err := repo.db.Exec(ctx, `INSERT INTO agent_knowledge_bases(agent_id,kb_id) VALUES($1,$2)`, current.ID, kb); err != nil {
		t.Fatal(err)
	}
	name := "Must not persist"
	invalid := []string{"not-installed"}
	err := repo.SaveDraft(ctx, current, Changes{Name: &name, KnowledgeBaseIDs: &invalid}, current.DraftRevision)
	if !errors.Is(err, ErrKnowledgeNotInstalled) {
		t.Fatalf("unexpected error: %v", err)
	}
	saved, err := repo.Get(ctx, current.ID, current.OwnerUserID, current.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != current.Name || saved.DraftRevision != current.DraftRevision || len(saved.KnowledgeBaseIDs) != 1 || saved.KnowledgeBaseIDs[0] != kb {
		t.Fatalf("failed save changed fields or knowledge: %#v", saved)
	}
}

func TestDraftBindingFailureRollsBackRevisionAndFields(t *testing.T) {
	repo, current := draftFixture(t)
	ctx := context.Background()
	name := "Must roll back"
	failed := errors.New("binding update failed")
	err := repo.SaveDraftWithBindings(ctx, current, Changes{Name: &name}, current.DraftRevision, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE agents SET system_prompt='partial binding change' WHERE id=$1`, current.ID)
		if err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("unexpected error: %v", err)
	}
	saved, err := repo.Get(ctx, current.ID, current.OwnerUserID, current.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != current.Name || saved.SystemPrompt != current.SystemPrompt || saved.DraftRevision != current.DraftRevision {
		t.Fatalf("binding failure escaped transaction: %#v", saved)
	}
}

func TestDraftRequiresRevision(t *testing.T) {
	repo, current := draftFixture(t)
	for _, revision := range []int64{0, -1} {
		if err := repo.SaveDraft(context.Background(), current, Changes{}, revision); !errors.Is(err, ErrRevisionRequired) {
			t.Fatalf("revision %d: %v", revision, err)
		}
	}
}
