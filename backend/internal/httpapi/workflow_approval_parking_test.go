package httpapi

import (
	"context"
	"cosmo/backend/internal/tools"
	"cosmo/backend/internal/workflows"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkflowParksAndResumesExactConsent(t *testing.T) {
	for _, mode := range []string{"approved", "rejected", "expired", "changed", "revoked", "claimed_crash", "two_approvals"} {
		t.Run(mode, func(t *testing.T) {
			s, agent, owner, _ := agentAccessFixture(t)
			ctx := context.Background()
			s.workflows = workflows.NewRepository(s.db, slog.Default())
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{AllowedHosts: []string{"127.0.0.1"}}, tools.SearchBackend{})
			var calls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
			defer target.Close()
			toolID, actionID, wfID := "tol_"+randomID(18), "act_"+randomID(18), "wf_"+randomID(18)
			graph := workflows.Graph{Nodes: []workflows.Node{{ID: "start", Kind: workflows.KindStart}, {ID: "read", Kind: workflows.KindTool, Config: map[string]any{"tool_id": toolID, "action_id": actionID}}, {ID: "end", Kind: workflows.KindEnd}}, Edges: []workflows.Edge{{ID: "a", Source: "start", Target: "read"}, {ID: "b", Source: "read", Target: "end"}}}
			raw, _ := json.Marshal(graph)
			for _, q := range []struct {
				query string
				args  []any
			}{
				{`INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,'http://example.invalid','fixture')`, []any{agent.WorkspaceID}},
				{`INSERT INTO tools(id,name,owner_user_id,owner_workspace_id,kind,base_url) VALUES($1,'Queue read',$2,$3,'http',$4)`, []any{toolID, owner.ID, agent.WorkspaceID, target.URL}},
				{`INSERT INTO tool_actions(id,tool_id,name,method,path) VALUES($1,$2,'read','POST','/')`, []any{actionID, toolID}},
				{`INSERT INTO workflows(id,name,owner_user_id,owner_workspace_id,graph) VALUES($1,'Queue test',$2,$3,$4)`, []any{wfID, owner.ID, agent.WorkspaceID, raw}},
			} {
				if _, err := s.db.Exec(ctx, q.query, q.args...); err != nil {
					t.Fatal(err)
				}
			}

			if mode == "two_approvals" {
				graph.Nodes = append(graph.Nodes[:2], workflows.Node{ID: "second", Kind: workflows.KindTool, Config: map[string]any{"tool_id": toolID, "action_id": actionID}}, graph.Nodes[2])
				graph.Edges = []workflows.Edge{{ID: "a", Source: "start", Target: "read"}, {ID: "b", Source: "read", Target: "second"}, {ID: "c", Source: "second", Target: "end"}}
				raw, _ = json.Marshal(graph)
				if _, err := s.db.Exec(ctx, `UPDATE workflows SET graph=$2 WHERE id=$1`, wfID, raw); err != nil {
					t.Fatal(err)
				}
			}
			item, err := s.workflows.Get(ctx, wfID, owner.ID, agent.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			execution, err := s.admitWorkflowExecution(context.WithValue(ctx, workflowQueueKey{}, "park-request"), item, owner.ID, "saved input", "fixture", "")
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.claimWorkflowJob(ctx)
			if err != nil || job == nil {
				t.Fatal(err)
			}
			s.runWorkflowJob(ctx, job)
			var status, approval, leaseOwner string
			if err = s.db.QueryRow(ctx, `SELECT status,approval_id,lease_owner FROM workflow_executions WHERE id=$1`, execution.ID).Scan(&status, &approval, &leaseOwner); err != nil {
				t.Fatal(err)
			}
			if status != "waiting_approval" || approval == "" || leaseOwner != "" || calls.Load() != 0 {
				t.Fatalf("not parked: status=%s owner=%s calls=%d", status, leaseOwner, calls.Load())
			}
			if job, err = s.claimWorkflowJob(ctx); err != nil || job != nil {
				t.Fatalf("pending consent claimed: %v", err)
			}
			// The same worker can finish another workflow while this one waits.
			other := item
			other.ID = wfID + "other"
			other.Graph = workflows.Graph{Nodes: []workflows.Node{{ID: "start", Kind: workflows.KindStart}, {ID: "end", Kind: workflows.KindEnd}}, Edges: []workflows.Edge{{ID: "a", Source: "start", Target: "end"}}}
			otherRaw, _ := json.Marshal(other.Graph)
			if _, err = s.db.Exec(ctx, `INSERT INTO workflows(id,name,owner_user_id,owner_workspace_id,graph) VALUES($1,'Other workflow',$2,$3,$4)`, other.ID, owner.ID, agent.WorkspaceID, otherRaw); err != nil {
				t.Fatal(err)
			}
			otherExecution, err := s.admitWorkflowExecution(context.WithValue(ctx, workflowQueueKey{}, "other-request"), other, owner.ID, "other input", "fixture", "")
			if err != nil {
				t.Fatal(err)
			}
			otherJob, err := s.claimWorkflowJob(ctx)
			if err != nil || otherJob == nil || otherJob.ID != otherExecution.ID {
				t.Fatalf("park blocked worker: %v", err)
			}
			s.runWorkflowJob(ctx, otherJob)
			if err = s.db.QueryRow(ctx, `SELECT status FROM workflow_executions WHERE id=$1`, otherExecution.ID).Scan(&status); err != nil || status != "succeeded" {
				t.Fatalf("other work not finished: %v", err)
			}
			if mode == "rejected" {
				_, err = s.db.Exec(ctx, `UPDATE tool_approvals SET status='rejected' WHERE id=$1`, approval)
			} else if mode == "expired" {
				_, err = s.db.Exec(ctx, `UPDATE tool_approvals SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, approval)
			} else {
				_, err = s.db.Exec(ctx, `UPDATE tool_approvals SET status='approved' WHERE id=$1`, approval)
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "changed" {
				_, err = s.db.Exec(ctx, `UPDATE tools SET updated_at=NOW() WHERE id=$1`, toolID)
			}
			if mode == "revoked" {
				_, err = s.db.Exec(ctx, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, owner.ID, agent.WorkspaceID)
			}
			if err != nil {
				t.Fatal(err)
			}
			job, err = s.claimWorkflowJob(ctx)
			if err != nil || job == nil {
				t.Fatalf("decision did not wake: %v", err)
			}
			if mode == "claimed_crash" {
				_, err = s.db.Exec(ctx, `UPDATE workflow_executions SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, execution.ID)
				if err != nil {
					t.Fatal(err)
				}
				workerCtx, stop := context.WithTimeout(ctx, 300*time.Millisecond)
				defer stop()
				s.RunWorkflowWorker(workerCtx)
			} else {
				s.runWorkflowJob(ctx, job)
			}
			wantCalls := int32(0)
			wantStatus := "interrupted"
			if mode == "approved" || mode == "two_approvals" {
				wantCalls = 1
				wantStatus = "succeeded"
			}
			if mode == "two_approvals" {
				var next string
				if err = s.db.QueryRow(ctx, `SELECT approval_id FROM workflow_executions WHERE id=$1 AND status='waiting_approval'`, execution.ID).Scan(&next); err != nil {
					t.Fatal(err)
				}
				if next == approval || calls.Load() != 1 {
					t.Fatal("earlier write repeated or approval reused")
				}
				if _, err = s.db.Exec(ctx, `UPDATE tool_approvals SET status='approved' WHERE id=$1`, next); err != nil {
					t.Fatal(err)
				}
				job, err = s.claimWorkflowJob(ctx)
				if err != nil || job == nil {
					t.Fatal(err)
				}
				s.runWorkflowJob(ctx, job)
				wantCalls = 2
			}
			if err = s.db.QueryRow(ctx, `SELECT status FROM workflow_executions WHERE id=$1`, execution.ID).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != wantStatus || calls.Load() != wantCalls {
				t.Fatalf("status=%s calls=%d; want=%s/%d", status, calls.Load(), wantStatus, wantCalls)
			}
		})
	}
}
