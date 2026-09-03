package httpapi

import (
	"context"
	"fmt"
	"strings"

	"cosmo/backend/internal/tools"
)

// What the model is told before anything else: who is asking, and where.
//
// Without it every member of every workspace got the same answer to a question
// whose answer depends on both - "who am I", "what do we do here", "who should
// I ask about X". The Profile tool answers the first deliberately; this makes
// the everyday case work without a round trip, and gives a workspace one place
// to say what it is for.
//
// Kept short on purpose. It is prepended to every turn, so anything written
// here is paid for in every message: facts, not prose.

// workspaceContext is the text a workspace's admins wrote for its members.
func (s *Server) workspaceContext(ctx context.Context, workspaceID string) string {
	if workspaceID == "" {
		return ""
	}
	var text string
	if err := s.db.QueryRow(ctx,
		`SELECT COALESCE(context, '') FROM workspaces WHERE id = $1`, workspaceID).Scan(&text); err != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

// conversationContext builds the block. It returns empty when there is nothing
// worth saying, so a turn with no facts does not carry an empty heading.
func conversationContext(caller tools.Caller, workspaceText string) string {
	var lines []string
	if caller.UserName != "" {
		who := caller.UserName
		if caller.UserEmail != "" {
			who += " (" + caller.UserEmail + ")"
		}
		lines = append(lines, "- Người đang hỏi: "+who)
	}
	if caller.WorkspaceName != "" {
		where := caller.WorkspaceName
		if caller.WorkspaceRole != "" {
			where += fmt.Sprintf(" (vai trò: %s)", caller.WorkspaceRole)
		}
		lines = append(lines, "- Workspace: "+where)
	}
	if workspaceText != "" {
		lines = append(lines, "- Bối cảnh workspace: "+workspaceText)
	}
	if len(lines) == 0 {
		return ""
	}
	return "Bối cảnh cuộc trò chuyện. Dùng khi liên quan, đừng nhắc lại nếu không được hỏi:\n" +
		strings.Join(lines, "\n")
}

// callerFor reads the few facts a turn is entitled to state about the person
// behind it: their account, and their standing in this workspace.
func (s *Server) callerFor(ctx context.Context, user User, workspaceID string) tools.Caller {
	caller := tools.Caller{
		UserID:      user.ID,
		UserName:    user.Name,
		UserEmail:   user.Email,
		UserRole:    user.Role,
		WorkspaceID: workspaceID,
	}
	if workspaceID == "" {
		return caller
	}
	// One read for both facts, and a failure leaves them empty rather than
	// half-stated: a wrong workspace in the prompt is worse than none.
	_ = s.db.QueryRow(ctx, `
		SELECT w.name, COALESCE(m.role, '')
		FROM workspaces w
		LEFT JOIN workspace_memberships m ON m.workspace_id = w.id AND m.user_id = $2
		WHERE w.id = $1`, workspaceID, user.ID).Scan(&caller.WorkspaceName, &caller.WorkspaceRole)
	return caller
}

// contextWindowFor is how much this model can be given at once, as the gateway
// reports it. Zero when it does not say, which the reader is shown as a count
// with nothing to compare against rather than as an invented limit.
func (s *Server) contextWindowFor(ctx context.Context, workspaceID, model string) int {
	if workspaceID == "" || model == "" {
		return 0
	}
	baseURL, _, apiKey, _, _, err := s.workspaceLLM(ctx, workspaceID)
	if err != nil || baseURL == "" {
		return 0
	}
	// Best effort and cheap to be wrong about: a gateway with no /model/info
	// simply leaves the reader with a count and no bar.
	return fetchGatewayModelMetadata(ctx, baseURL, apiKey)[model].ContextWindow
}

// describePassages says what a retrieval actually brought back: how many
// passages, and which documents they came from. "Đã tra Knowledge Base" on its
// own tells a reader nothing they could check.
func describePassages(passages []knowledgePassage) string {
	if len(passages) == 0 {
		return "không có đoạn nào khớp"
	}
	titles := []string{}
	seen := map[string]bool{}
	for _, passage := range passages {
		name := strings.TrimSpace(passage.Title)
		if name == "" {
			name = strings.TrimSpace(passage.Source)
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		// Three names is a sentence; ten is a list nobody reads in a status
		// line, and the citations below carry the rest anyway.
		if len(titles) == 3 {
			titles = append(titles, "…")
			break
		}
		titles = append(titles, name)
	}
	return fmt.Sprintf("%d đoạn · %s", len(passages), strings.Join(titles, ", "))
}

// describeToolSet names what this turn is allowed to reach for, and where that
// permission came from - an agent's own attachments, or what the workspace
// installed and switched on.
func describeToolSet(set toolSet) string {
	names := make([]string, 0, len(set.tools))
	for _, tool := range set.tools {
		names = append(names, tool.Name)
		if len(names) == 6 {
			names = append(names, "…")
			break
		}
	}
	origin := "workspace"
	if set.source == "agent" {
		origin = "agent"
	}
	return fmt.Sprintf("%s: %s", origin, strings.Join(names, ", "))
}
