package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"cosmo/backend/internal/agents"
	"cosmo/backend/internal/tools"
)

func TestPublishedAgentRequiresAndPreservesToolVersions(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	ctx := context.Background()
	repo := tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{}, tools.SearchBackend{})
	toolID := "tol_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO tools(id,name,owner_user_id,owner_workspace_id,base_url) VALUES($1,'Dependency test',$2,$3,'https://example.com')`, toolID, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	action, err := repo.SaveAction(ctx, toolID, "", tools.Action{Name: "lookup", Method: "GET", Path: "/original", Parameters: []tools.Parameter{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetAgentTools(ctx, agent.ID, owner.ID, agent.WorkspaceID, []string{toolID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.agents.Publish(ctx, agent.ID, owner.ID, "invalid"); !errors.Is(err, agents.ErrToolReleaseRequired) {
		t.Fatalf("published draft dependency: %v", err)
	}
	toolVersion, err := repo.Publish(ctx, toolID, owner.ID, "v1")
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.agents.Publish(ctx, agent.ID, owner.ID, "v1")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := s.agents.RuntimeForVersion(ctx, agent.ID, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ToolVersions[toolID] != toolVersion.ID {
		t.Fatalf("version not pinned: %#v", runtime.ToolVersions)
	}
	if _, err := s.db.Exec(ctx, `UPDATE tool_actions SET path='/changed' WHERE id=$1`, action.ID); err != nil {
		t.Fatal(err)
	}
	_, frozen, err := repo.PinnedTools(ctx, agent.ID, runtime.ToolIDs, runtime.ToolVersions)
	if err != nil {
		t.Fatal(err)
	}
	if len(frozen[toolID]) != 1 || frozen[toolID][0].Path != "/original" {
		t.Fatalf("draft changed release: %#v", frozen)
	}
	for _, pins := range []map[string]string{nil, {toolID: "missing-version"}} {
		if _, _, err := repo.PinnedTools(ctx, agent.ID, runtime.ToolIDs, pins); !errors.Is(err, tools.ErrPinnedVersionMissing) {
			t.Fatalf("fell back to draft: %v", err)
		}
	}
	// An explicit empty published set remains empty even when the draft has tools.
	if list, _, err := repo.PinnedTools(ctx, agent.ID, []string{}, nil); err != nil || len(list) != 0 {
		t.Fatalf("empty release inherited tools: %#v %v", list, err)
	}
	// Only editor draft execution is allowed to resolve current actions.
	if _, draft, err := repo.PinnedTools(ctx, agent.ID, nil, nil); err != nil || draft[toolID][0].Path != "/changed" {
		t.Fatalf("draft unavailable: %#v %v", draft, err)
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM tools WHERE id=$1`, toolID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.PinnedTools(ctx, agent.ID, runtime.ToolIDs, runtime.ToolVersions); !errors.Is(err, tools.ErrPinnedVersionMissing) {
		t.Fatalf("deleted dependency ignored: %v", err)
	}
}
