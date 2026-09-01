package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cosmo/backend/internal/modelgateway"

	"github.com/go-chi/chi/v5"
)

const (
	// agentPrivate keeps an agent to its author. agentShared offers it to
	// everyone in the workspace it was made in.
	agentPrivate = "private"
	agentShared  = "workspace"

	maxAgentKnowledgeBases  = 5
	maxAgentPresetQuestions = 10

	// A memory rides along on every turn, so it is capped to keep it from
	// crowding out the conversation it is meant to support.
	maxAgentMemoryRunes = 2000

	// Three is what the Experience tab promises the reader.
	maxAgentSuggestions = 3
)

// suggestionInstruction asks for bare questions, one per line. The reply is
// split on newlines and anything that does not look like a question is
// dropped, so a model that adds a preamble degrades to fewer suggestions
// rather than putting prose in a button.
const suggestionInstruction = `Dựa trên đoạn hội thoại dưới đây, hãy đề xuất 3 câu hỏi tiếp theo mà người dùng có thể muốn hỏi.
Mỗi câu một dòng, không đánh số, không thêm lời dẫn, không dùng dấu gạch đầu dòng.
Viết bằng ngôn ngữ mà người dùng đang dùng.

Người dùng hỏi:
%s

Agent trả lời:
%s`

// agentMemoryHeader introduces the memory where it is injected into a turn.
const agentMemoryHeader = `Điều đã biết về người dùng này:
`

// memoryInstruction asks for the whole memory back rather than a diff: merging
// is the model's job, and a diff would need a second pass to apply.
const memoryInstruction = `Bạn đang duy trì trí nhớ dài hạn về một người dùng.
Ghi lại những điều bền vững, hữu ích cho các lần trò chuyện sau: vai trò, lĩnh vực phụ trách,
cách họ muốn được trả lời, các ràng buộc họ đã nêu.
Bỏ qua nội dung nhất thời của riêng câu hỏi này.
Trả về TOÀN BỘ trí nhớ sau khi cập nhật, mỗi ý một dòng, tối đa 15 dòng, không thêm lời dẫn.
Nếu không có gì đáng nhớ, trả về đúng phần trí nhớ hiện có.

Trí nhớ hiện có:
%s

Người dùng hỏi:
%s

Agent trả lời:
%s`

var (
	errAgentKnowledgeSave   = errors.New("Không thể lưu knowledge base cho agent.")
	errAgentKnowledgeAccess = errors.New("Knowledge base chưa được cài vào workspace này.")
)

// Agent is a saved chat configuration: who it is, which model answers as it,
// what it is told to do, and what a reader sees before the first question.
type Agent struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Introduction          string    `json:"introduction"`
	Avatar                string    `json:"avatar"`
	Tags                  []string  `json:"tags"`
	OwnerUserID           string    `json:"owner_user_id"`
	OwnerName             string    `json:"owner_name"`
	WorkspaceID           string    `json:"workspace_id"`
	Visibility            string    `json:"visibility"`
	Model                 string    `json:"model"`
	SystemPrompt          string    `json:"system_prompt"`
	OpeningLine           string    `json:"opening_line"`
	PresetQuestions       []string  `json:"preset_questions"`
	HasSuggestedQuestions bool      `json:"has_suggested_questions"`
	IsMemoryEnabled       bool      `json:"is_memory_enabled"`
	KnowledgeBaseIDs      []string  `json:"knowledge_base_ids"`
	IsEditable            bool      `json:"is_editable"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

const agentColumns = `
	a.id, a.name, a.introduction, a.avatar, a.tags, COALESCE(a.owner_user_id, ''),
	COALESCE(u.name, ''), a.owner_workspace_id, a.visibility, a.model, a.system_prompt,
	a.opening_line, a.preset_questions, a.has_suggested_questions, a.is_memory_enabled,
	a.created_at, a.updated_at`

// visibleAgentSQL is the one place that decides who may see an agent: everyone
// in the workspace sees a shared one, only the author sees a private one.
const visibleAgentSQL = `a.owner_workspace_id = $2 AND (a.visibility = 'workspace' OR a.owner_user_id = $1)`

func scanAgent(scan func(...any) error) (Agent, error) {
	var item Agent
	var tags, presets []byte
	err := scan(&item.ID, &item.Name, &item.Introduction, &item.Avatar, &tags, &item.OwnerUserID,
		&item.OwnerName, &item.WorkspaceID, &item.Visibility, &item.Model, &item.SystemPrompt,
		&item.OpeningLine, &presets, &item.HasSuggestedQuestions, &item.IsMemoryEnabled,
		&item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return Agent{}, err
	}
	item.Tags = decodeStringList(tags)
	item.PresetQuestions = decodeStringList(presets)
	item.KnowledgeBaseIDs = []string{}
	return item, nil
}

// decodeStringList turns a jsonb column into a slice that is never nil, so the
// response carries [] rather than null and the client needs no special case.
func decodeStringList(raw []byte) []string {
	values := []string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &values)
	}
	if values == nil {
		values = []string{}
	}
	return values
}

// cleanStringList trims, drops blanks and truncates, so a list arriving from a
// form cannot store empty entries or grow without bound.
func cleanStringList(values []string, limit, maxRunes int) []string {
	cleaned := []string{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxRunes {
			trimmed = string([]rune(trimmed)[:maxRunes])
		}
		cleaned = append(cleaned, trimmed)
		if len(cleaned) >= limit {
			break
		}
	}
	return cleaned
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := s.memberWorkspace(r.Context(), user.ID, r.URL.Query().Get("workspace"))
	if workspaceID == "" {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}

	rows, err := s.db.Query(r.Context(), `
		SELECT `+agentColumns+`
		FROM agents a
		LEFT JOIN users u ON u.id = a.owner_user_id
		WHERE `+visibleAgentSQL+`
		ORDER BY a.updated_at DESC`, user.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách agent.")
		return
	}
	defer rows.Close()

	isAdmin := s.isWorkspaceAdmin(r.Context(), user, workspaceID)
	items := []Agent{}
	for rows.Next() {
		if item, err := scanAgent(rows.Scan); err == nil {
			item.IsEditable = item.OwnerUserID == user.ID || isAdmin
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": items})
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	var input struct {
		Name         string   `json:"name"`
		Introduction string   `json:"introduction"`
		Avatar       string   `json:"avatar"`
		Tags         []string `json:"tags"`
		Visibility   string   `json:"visibility"`
		WorkspaceID  string   `json:"workspace_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 120 {
		writeError(w, http.StatusBadRequest, "Tên agent phải từ 1 đến 120 ký tự.")
		return
	}
	input.Introduction = strings.TrimSpace(input.Introduction)
	if len([]rune(input.Introduction)) > 512 {
		writeError(w, http.StatusBadRequest, "Giới thiệu tối đa 512 ký tự.")
		return
	}
	visibility := agentPrivate
	if input.Visibility == agentShared {
		visibility = agentShared
	}

	workspaceID := s.memberWorkspace(r.Context(), user.ID, input.WorkspaceID)
	if workspaceID == "" {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}

	tags, _ := json.Marshal(cleanStringList(input.Tags, 10, 40))
	agentID := "agt_" + randomID(18)
	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO agents(id, name, introduction, avatar, tags, owner_user_id, owner_workspace_id, visibility)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8)`,
		agentID, input.Name, input.Introduction, strings.TrimSpace(input.Avatar), tags,
		user.ID, workspaceID, visibility); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo agent.")
		return
	}
	s.writeAgent(w, r, agentID, workspaceID, http.StatusCreated)
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := s.memberWorkspace(r.Context(), user.ID, r.URL.Query().Get("workspace"))
	if workspaceID == "" {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	s.writeAgent(w, r, chi.URLParam(r, "agentID"), workspaceID, http.StatusOK)
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	current, workspaceID, ok := s.agentForWrite(w, r, agentID)
	if !ok {
		return
	}

	// Every field is optional: the editor saves one tab at a time, and a field
	// left out must keep what is stored rather than blank it.
	var input struct {
		Name                  *string   `json:"name"`
		Introduction          *string   `json:"introduction"`
		Avatar                *string   `json:"avatar"`
		Tags                  *[]string `json:"tags"`
		Visibility            *string   `json:"visibility"`
		Model                 *string   `json:"model"`
		SystemPrompt          *string   `json:"system_prompt"`
		OpeningLine           *string   `json:"opening_line"`
		PresetQuestions       *[]string `json:"preset_questions"`
		HasSuggestedQuestions *bool     `json:"has_suggested_questions"`
		IsMemoryEnabled       *bool     `json:"is_memory_enabled"`
		KnowledgeBaseIDs      *[]string `json:"knowledge_base_ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}

	name := current.Name
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
		if name == "" || len([]rune(name)) > 120 {
			writeError(w, http.StatusBadRequest, "Tên agent phải từ 1 đến 120 ký tự.")
			return
		}
	}
	introduction := current.Introduction
	if input.Introduction != nil {
		introduction = strings.TrimSpace(*input.Introduction)
		if len([]rune(introduction)) > 512 {
			writeError(w, http.StatusBadRequest, "Giới thiệu tối đa 512 ký tự.")
			return
		}
	}
	visibility := current.Visibility
	if input.Visibility != nil && (*input.Visibility == agentPrivate || *input.Visibility == agentShared) {
		visibility = *input.Visibility
	}
	avatar := current.Avatar
	if input.Avatar != nil {
		avatar = strings.TrimSpace(*input.Avatar)
	}
	model := current.Model
	if input.Model != nil {
		model = strings.TrimSpace(*input.Model)
	}
	systemPrompt := current.SystemPrompt
	if input.SystemPrompt != nil {
		systemPrompt = *input.SystemPrompt
	}
	openingLine := current.OpeningLine
	if input.OpeningLine != nil {
		openingLine = *input.OpeningLine
	}
	tags := current.Tags
	if input.Tags != nil {
		tags = cleanStringList(*input.Tags, 10, 40)
	}
	presets := current.PresetQuestions
	if input.PresetQuestions != nil {
		presets = cleanStringList(*input.PresetQuestions, maxAgentPresetQuestions, 200)
	}
	hasSuggested := current.HasSuggestedQuestions
	if input.HasSuggestedQuestions != nil {
		hasSuggested = *input.HasSuggestedQuestions
	}
	isMemory := current.IsMemoryEnabled
	if input.IsMemoryEnabled != nil {
		isMemory = *input.IsMemoryEnabled
	}

	tagsJSON, _ := json.Marshal(tags)
	presetsJSON, _ := json.Marshal(presets)
	if _, err := s.db.Exec(r.Context(), `
		UPDATE agents SET name = $2, introduction = $3, avatar = $4, tags = $5, visibility = $6,
			model = $7, system_prompt = $8, opening_line = $9, preset_questions = $10,
			has_suggested_questions = $11, is_memory_enabled = $12, updated_at = NOW()
		WHERE id = $1`,
		agentID, name, introduction, avatar, tagsJSON, visibility, model, systemPrompt,
		openingLine, presetsJSON, hasSuggested, isMemory); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu agent.")
		return
	}

	if input.KnowledgeBaseIDs != nil {
		if err := s.setAgentKnowledgeBases(r.Context(), agentID, workspaceID, *input.KnowledgeBaseIDs); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.writeAgent(w, r, agentID, workspaceID, http.StatusOK)
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	if _, _, ok := s.agentForWrite(w, r, agentID); !ok {
		return
	}
	if _, err := s.db.Exec(r.Context(), `DELETE FROM agents WHERE id = $1`, agentID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá agent.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setAgentKnowledgeBases replaces the agent's reading list. A base has to be
// installed in the workspace before an agent there may read it: installing is
// the act that puts a base at the workspace's disposal, and an agent is one
// more thing in the workspace that draws on it. Merely being *visible* - a
// base published to everyone but not installed here - is not enough, or an
// agent would become a way to read a base the workspace never took on.
func (s *Server) setAgentKnowledgeBases(ctx context.Context, agentID, workspaceID string, kbIDs []string) error {
	wanted := cleanStringList(kbIDs, maxAgentKnowledgeBases, 64)
	if _, err := s.db.Exec(ctx, `DELETE FROM agent_knowledge_bases WHERE agent_id = $1`, agentID); err != nil {
		return errAgentKnowledgeSave
	}
	for _, kbID := range wanted {
		var installed bool
		if err := s.db.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM knowledge_mounts km
				WHERE km.kb_id = $1 AND km.target_type = 'workspace' AND km.target_id = $2
			)`, kbID, workspaceID).Scan(&installed); err != nil || !installed {
			return errAgentKnowledgeAccess
		}
		if _, err := s.db.Exec(ctx, `
			INSERT INTO agent_knowledge_bases(agent_id, kb_id) VALUES($1, $2)
			ON CONFLICT DO NOTHING`, agentID, kbID); err != nil {
			return errAgentKnowledgeSave
		}
	}
	return nil
}

// agentForWrite loads an agent the caller is allowed to change: its author, or
// an administrator of the workspace it lives in.
func (s *Server) agentForWrite(w http.ResponseWriter, r *http.Request, agentID string) (Agent, string, bool) {
	user := currentUser(r.Context())
	workspaceID := s.memberWorkspace(r.Context(), user.ID, r.URL.Query().Get("workspace"))
	if workspaceID == "" {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return Agent{}, "", false
	}
	item, err := s.loadAgent(r.Context(), agentID, user.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy agent.")
		return Agent{}, "", false
	}
	if item.OwnerUserID != user.ID && !s.isWorkspaceAdmin(r.Context(), user, workspaceID) {
		writeError(w, http.StatusForbidden, "Chỉ người tạo agent hoặc quản trị workspace mới sửa được.")
		return Agent{}, "", false
	}
	return item, workspaceID, true
}

func (s *Server) loadAgent(ctx context.Context, agentID, userID, workspaceID string) (Agent, error) {
	row := s.db.QueryRow(ctx, `
		SELECT `+agentColumns+`
		FROM agents a
		LEFT JOIN users u ON u.id = a.owner_user_id
		WHERE a.id = $3 AND `+visibleAgentSQL, userID, workspaceID, agentID)
	item, err := scanAgent(row.Scan)
	if err != nil {
		return Agent{}, err
	}
	rows, err := s.db.Query(ctx, `SELECT kb_id FROM agent_knowledge_bases WHERE agent_id = $1 ORDER BY created_at`, agentID)
	if err != nil {
		return item, nil
	}
	defer rows.Close()
	for rows.Next() {
		var kbID string
		if rows.Scan(&kbID) == nil {
			item.KnowledgeBaseIDs = append(item.KnowledgeBaseIDs, kbID)
		}
	}
	return item, nil
}

func (s *Server) writeAgent(w http.ResponseWriter, r *http.Request, agentID, workspaceID string, status int) {
	user := currentUser(r.Context())
	item, err := s.loadAgent(r.Context(), agentID, user.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy agent.")
		return
	}
	item.IsEditable = item.OwnerUserID == user.ID || s.isWorkspaceAdmin(r.Context(), user, workspaceID)
	writeJSON(w, status, map[string]any{"agent": item})
}

// loadAgentForRun reads the configuration a conversation runs on. It asks no
// visibility question on purpose: the right to use an agent was settled when
// the conversation was created, and a later change to who may *see* the agent
// must not silently strip an ongoing conversation of its instructions.
func (s *Server) loadAgentForRun(ctx context.Context, agentID string) (Agent, error) {
	var item Agent
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(model, ''), COALESCE(system_prompt, ''), is_memory_enabled, has_suggested_questions
		FROM agents WHERE id = $1`, agentID).Scan(&item.Model, &item.SystemPrompt, &item.IsMemoryEnabled, &item.HasSuggestedQuestions)
	if err != nil {
		return Agent{}, err
	}
	item.KnowledgeBaseIDs = []string{}
	rows, err := s.db.Query(ctx, `SELECT kb_id FROM agent_knowledge_bases WHERE agent_id = $1`, agentID)
	if err != nil {
		return item, nil
	}
	defer rows.Close()
	for rows.Next() {
		var kbID string
		if rows.Scan(&kbID) == nil {
			item.KnowledgeBaseIDs = append(item.KnowledgeBaseIDs, kbID)
		}
	}
	return item, nil
}

// listAgentConversations and startAgentConversation are the agent's own chat
// history. They write the same conversations table the general chat uses, but
// stamped with agent_id, and the general chat filters that column out - so the
// two surfaces share every message mechanism without ever sharing a list.
func (s *Server) listAgentConversations(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	agentID := chi.URLParam(r, "agentID")
	workspaceID := s.memberWorkspace(r.Context(), user.ID, r.URL.Query().Get("workspace"))
	if workspaceID == "" {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	if _, err := s.loadAgent(r.Context(), agentID, user.ID, workspaceID); err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy agent.")
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT id, workspace_id, title, created_at, updated_at
		FROM conversations
		WHERE user_id = $1 AND workspace_id = $2 AND agent_id = $3
		ORDER BY updated_at DESC LIMIT 100`, user.ID, workspaceID, agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải hội thoại của agent.")
		return
	}
	defer rows.Close()
	items := []Conversation{}
	for rows.Next() {
		var item Conversation
		if rows.Scan(&item.ID, &item.WorkspaceID, &item.Title, &item.CreatedAt, &item.UpdatedAt) == nil {
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": items})
}

func (s *Server) startAgentConversation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	agentID := chi.URLParam(r, "agentID")
	workspaceID := s.memberWorkspace(r.Context(), user.ID, r.URL.Query().Get("workspace"))
	if workspaceID == "" {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	// Seeing the agent is what grants the right to talk to it, so a private
	// agent cannot be addressed by anyone but its author.
	agent, err := s.loadAgent(r.Context(), agentID, user.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy agent.")
		return
	}
	conversationID := "cnv_" + randomID(18)
	title := agent.Name
	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO conversations(id, user_id, workspace_id, title, agent_id)
		VALUES($1, $2, $3, $4, $5)`, conversationID, user.ID, workspaceID, title, agentID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo hội thoại.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"conversation": Conversation{
		ID: conversationID, WorkspaceID: workspaceID, Title: title,
	}})
}

// agentMemory is what this agent has learned about this person. A missing row
// is normal and means nothing has been learned yet.
func (s *Server) agentMemory(ctx context.Context, agentID, userID string) string {
	var content string
	if err := s.db.QueryRow(ctx, `
		SELECT content FROM agent_memories WHERE agent_id = $1 AND user_id = $2`,
		agentID, userID).Scan(&content); err != nil {
		return ""
	}
	return strings.TrimSpace(content)
}

// rememberExchange folds the latest question and answer into what the agent
// knows about this person. It runs after the reply has been sent, on a context
// of its own: the request is finished by then, so inheriting its context would
// cancel the call every time. A failure is logged and dropped - forgetting is
// a far smaller harm than making the reader wait on a turn already answered.
func (s *Server) rememberExchange(agentID, userID, question, answer string, models *modelgateway.Client, options modelgateway.Options) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	existing := s.agentMemory(ctx, agentID, userID)
	prompt := fmt.Sprintf(memoryInstruction, existing, question, answer)
	updated, err := models.Complete(ctx, []modelgateway.Message{{Role: "user", Content: prompt}}, options)
	if err != nil {
		s.logger.Error("update agent memory", "agent_id", agentID, "error", err)
		return
	}
	updated = strings.TrimSpace(updated)
	if updated == "" || updated == existing {
		return
	}
	if len([]rune(updated)) > maxAgentMemoryRunes {
		updated = string([]rune(updated)[:maxAgentMemoryRunes])
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO agent_memories(agent_id, user_id, content, updated_at)
		VALUES($1, $2, $3, NOW())
		ON CONFLICT (agent_id, user_id) DO UPDATE SET content = EXCLUDED.content, updated_at = NOW()`,
		agentID, userID, updated); err != nil {
		s.logger.Error("save agent memory", "agent_id", agentID, "error", err)
	}
}

// suggestFollowUps proposes what the reader might ask next. It runs inside the
// request, because the suggestions are part of the reply the reader is waiting
// on, but under a short timeout: a slow suggestion pass must not hold up a
// turn that has already been answered, so it gives up and returns nothing.
func (s *Server) suggestFollowUps(ctx context.Context, question, answer string, models *modelgateway.Client, options modelgateway.Options) []string {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	reply, err := models.Complete(ctx, []modelgateway.Message{
		{Role: "user", Content: fmt.Sprintf(suggestionInstruction, question, answer)},
	}, options)
	if err != nil {
		s.logger.Error("suggest follow-up questions", "error", err)
		return nil
	}

	suggestions := []string{}
	for _, line := range strings.Split(reply, "\n") {
		line = strings.TrimSpace(line)
		// Strip the bullet or number a model adds despite being asked not to.
		line = strings.TrimLeft(line, "-*• 0123456789.)")
		line = strings.TrimSpace(line)
		if line == "" || len([]rune(line)) > 200 {
			continue
		}
		suggestions = append(suggestions, line)
		if len(suggestions) == maxAgentSuggestions {
			break
		}
	}
	return suggestions
}
