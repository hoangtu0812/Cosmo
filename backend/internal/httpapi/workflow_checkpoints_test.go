package httpapi

import (
	"context"
	"cosmo/backend/internal/tools"
	"cosmo/backend/internal/workflows"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestWorkflowResumeFencesUnknownEffectsAndRuntimeChanges(t *testing.T) {
	for _, mode := range []string{"between", "pending", "succeeded", "uncertain", "no_approval", "changed", "actor", "old_writer"} {
		t.Run(mode, func(t *testing.T) {
			s, agent, owner, member := agentAccessFixture(t)
			ctx := context.Background()
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{}, tools.SearchBackend{})
			toolID := "tol_" + randomID(18)
			actionID := "act_" + randomID(18)
			wfID := "wf_" + randomID(18)
			for _, query := range []struct {
				sql  string
				args []any
			}{
				{`INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,'http://example.invalid','test')`, []any{agent.WorkspaceID}},
				{`INSERT INTO tools(id,name,owner_user_id,owner_workspace_id,kind,base_url) VALUES($1,'Checkpoint test',$2,$3,'http','http://example.invalid')`, []any{toolID, owner.ID, agent.WorkspaceID}},
				{`INSERT INTO tool_actions(id,tool_id,name,method,path) VALUES($1,$2,'submit','POST','/')`, []any{actionID, toolID}},
				{`INSERT INTO workflows(id,name,owner_user_id,owner_workspace_id) VALUES($1,'Checkpoint test',$2,$3)`, []any{wfID, owner.ID, agent.WorkspaceID}},
			} {
				if _, err := s.db.Exec(ctx, query.sql, query.args...); err != nil {
					t.Fatal(err)
				}
			}
			item := workflows.Workflow{ID: wfID, WorkspaceID: agent.WorkspaceID, Graph: workflows.Graph{Nodes: []workflows.Node{{ID: "tool", Kind: workflows.KindTool, Config: map[string]any{"tool_id": toolID, "action_id": actionID}}}}}
			exec, err := s.admitWorkflowExecution(ctx, item, owner.ID, "saved input", "test", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.admitWorkflowExecution(ctx, item, owner.ID, "second", "test", ""); err == nil {
				t.Fatal("concurrent execution admitted")
			}
			if mode != "between" && mode != "old_writer" {
				if err = s.saveWorkflowCheckpoint(ctx, exec, workflows.Step{NodeID: "tool", Kind: "tool", Status: workflows.StatusRunning}); err != nil {
					t.Fatal(err)
				}
				if mode != "no_approval" {
					approval := "tap_" + randomID(18)
					if _, err = s.db.Exec(ctx, `INSERT INTO tool_approvals(id,actor_id,workspace_id,tool_id,source_kind,source_id,request,status,expires_at) VALUES($1,$2,$3,$4,'workflow',$5,'{}','pending',NOW()+INTERVAL '1 minute')`, approval, owner.ID, agent.WorkspaceID, toolID, wfID); err != nil {
						t.Fatal(err)
					}
					if _, err = s.db.Exec(ctx, `UPDATE workflow_executions SET approval_id=$2 WHERE id=$1`, exec.ID, approval); err != nil {
						t.Fatal(err)
					}
					if mode == "succeeded" || mode == "uncertain" {
						raw, _ := json.Marshal(tools.CallResult{Status: 200, Body: "original response"})
						if _, err = s.db.Exec(ctx, `INSERT INTO tool_write_operations(id,tool_id,action_id,actor_id,workspace_id,idempotency_key,request_hash,status,result) VALUES($1,$2,$3,$4,$5,$6,'test',$7,$8)`, "two_"+randomID(18), toolID, actionID, owner.ID, agent.WorkspaceID, approval, mode, string(raw)); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if _, err = s.db.Exec(ctx, `UPDATE workflow_executions SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, exec.ID); err != nil {
				t.Fatal(err)
			}
			if mode == "changed" {
				item.Graph.Nodes[0].Name = "changed"
			}
			actor := owner.ID
			if mode == "actor" {
				actor = member.ID
			}
			resumed, err := s.admitWorkflowExecution(ctx, item, actor, "different input", "test", exec.ID)
			allowed := mode == "between" || mode == "pending" || mode == "succeeded" || mode == "old_writer"
			if allowed {
				if err != nil {
					t.Fatal(err)
				}
				if resumed.Input != "saved input" {
					t.Fatal("replaced saved input")
				}
				if mode == "succeeded" && resumed.Completed["tool"].Output != "original response" {
					t.Fatal("lost successful ledger result")
				}
				if mode == "old_writer" && s.saveWorkflowCheckpoint(ctx, exec, workflows.Step{NodeID: "tool", Status: workflows.StatusRunning}) == nil {
					t.Fatal("stale executor wrote checkpoint")
				}
			} else if err == nil {
				t.Fatal("unsafe resume allowed")
			}
		})
	}
}
