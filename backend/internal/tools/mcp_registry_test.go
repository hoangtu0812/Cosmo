package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func registryAction(name string) Action {
	raw, _ := json.Marshal(map[string]any{"name": name, "inputSchema": map[string]any{"type": "object"}})
	return Action{Name: name, Method: "POST", Path: "/", MCPTool: raw}
}

func TestMCPReplacementRollbackAndRevision(t *testing.T) {
	repo, tool, _ := mcpDatabaseFixture(t, AuthNone, "")
	ctx := context.Background()
	original, err := repo.SaveAction(ctx, tool.ID, "", registryAction("original"))
	if err != nil {
		t.Fatal(err)
	}
	repo.db.QueryRow(ctx, `SELECT updated_at FROM tools WHERE id=$1`, tool.ID).Scan(&tool.UpdatedAt)
	// The second insert fails the actual database's case-insensitive uniqueness
	// constraint after the first insert. Neither deletes nor inserts may persist.
	_, _, err = repo.ReplaceMCPActions(ctx, tool, []Action{registryAction("duplicate"), registryAction("DUPLICATE")})
	if err == nil {
		t.Fatal("expected insert failure")
	}
	current, _ := repo.Actions(ctx, tool.ID)
	if len(current) != 1 || current[0].ID != original.ID {
		t.Fatal("partial replacement escaped rollback")
	}
	saved, _, err := repo.ReplaceMCPActions(ctx, tool, []Action{registryAction("original"), registryAction("new")})
	if err != nil || len(saved) != 2 || saved[0].ID != original.ID {
		t.Fatalf("successful registry/identity: %v", err)
	}
	if _, _, err = repo.ReplaceMCPActions(ctx, tool, nil); !errors.Is(err, ErrMCPDiscoveryChanged) {
		t.Fatalf("stale discovery overwrote registry: %v", err)
	}
}

func TestPublishAndMCPReplacementObserveCompleteRegistry(t *testing.T) {
	repo, tool, user := mcpDatabaseFixture(t, AuthNone, "")
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if _, err := repo.SaveAction(ctx, tool.ID, "", registryAction(fmt.Sprintf("old_%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	repo.db.QueryRow(ctx, `SELECT updated_at FROM tools WHERE id=$1`, tool.ID).Scan(&tool.UpdatedAt)
	next := []Action{}
	for i := 0; i < 8; i++ {
		next = append(next, registryAction(fmt.Sprintf("new_%d", i)))
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, err := repo.ReplaceMCPActions(ctx, tool, next)
		if err != nil && !errors.Is(err, ErrMCPDiscoveryChanged) {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		version, err := repo.Publish(ctx, tool.ID, user, "")
		if err != nil {
			t.Error(err)
			return
		}
		if len(version.Actions) != 8 {
			t.Errorf("published partial registry: %d", len(version.Actions))
		}
		prefix := version.Actions[0].Name[:3]
		for _, a := range version.Actions {
			if a.Name[:3] != prefix {
				t.Error("mixed registry")
			}
		}
	}()
	wg.Wait()
}
