package agents

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

func NewRepository(db *pgxpool.Pool, logger *slog.Logger) *Repository {
	return &Repository{db: db, logger: logger}
}

func newID(prefix string) string {
	data := make([]byte, 18)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(data)
}

const columns = `
	a.id, a.name, a.introduction, a.avatar, a.tags, COALESCE(a.owner_user_id, ''),
	COALESCE(u.name, ''), a.owner_workspace_id, a.visibility, a.model, a.system_prompt,
	a.opening_line, a.preset_questions, a.has_suggested_questions, a.is_memory_enabled,
	(a.avatar_image IS NOT NULL), a.created_at, a.updated_at`

// visibleSQL is the one place that decides who may see an agent: everyone in
// the workspace sees a shared one, only the author sees a private one. $1 is
// the caller and $2 the workspace.
const visibleSQL = `a.owner_workspace_id = $2 AND (a.visibility = 'workspace' OR a.owner_user_id = $1)`

func scan(row func(...any) error) (Agent, error) {
	var item Agent
	var tags, presets []byte
	err := row(&item.ID, &item.Name, &item.Introduction, &item.Avatar, &tags, &item.OwnerUserID,
		&item.OwnerName, &item.WorkspaceID, &item.Visibility, &item.Model, &item.SystemPrompt,
		&item.OpeningLine, &presets, &item.HasSuggestedQuestions, &item.IsMemoryEnabled,
		&item.HasAvatarImage, &item.CreatedAt, &item.UpdatedAt)
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

// CleanStringList trims, drops blanks and truncates, so a list arriving from a
// form cannot store empty entries or grow without bound.
func CleanStringList(values []string, limit, maxRunes int) []string {
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

// ValidateName and ValidateIntroduction are exported because creating and
// updating both apply them, and a caller may want to reject before writing.
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > MaxNameRunes {
		return "", ErrNameLength
	}
	return name, nil
}

func ValidateIntroduction(introduction string) (string, error) {
	introduction = strings.TrimSpace(introduction)
	if len([]rune(introduction)) > MaxIntroRunes {
		return "", ErrIntroLength
	}
	return introduction, nil
}

// NormalizeVisibility falls back to private: an unrecognised value must never
// widen who can see an agent.
func NormalizeVisibility(visibility string) string {
	if visibility == Shared {
		return Shared
	}
	return Private
}

func (repository *Repository) List(ctx context.Context, userID, workspaceID string) ([]Agent, error) {
	rows, err := repository.db.Query(ctx, `
		SELECT `+columns+`
		FROM agents a
		LEFT JOIN users u ON u.id = a.owner_user_id
		WHERE `+visibleSQL+`
		ORDER BY a.updated_at DESC`, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []Agent{}
	for rows.Next() {
		if item, err := scan(rows.Scan); err == nil {
			items = append(items, item)
		}
	}
	return items, rows.Err()
}

func (repository *Repository) Create(ctx context.Context, input NewAgent) (string, error) {
	name, err := ValidateName(input.Name)
	if err != nil {
		return "", err
	}
	introduction, err := ValidateIntroduction(input.Introduction)
	if err != nil {
		return "", err
	}
	tags, _ := json.Marshal(CleanStringList(input.Tags, 10, 40))
	agentID := newID("agt_")
	_, err = repository.db.Exec(ctx, `
		INSERT INTO agents(id, name, introduction, avatar, tags, owner_user_id, owner_workspace_id, visibility)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8)`,
		agentID, name, introduction, strings.TrimSpace(input.Avatar), tags,
		input.OwnerUserID, input.WorkspaceID, NormalizeVisibility(input.Visibility))
	if err != nil {
		return "", err
	}
	return agentID, nil
}

// Get returns an agent the caller is allowed to see, and its reading list.
func (repository *Repository) Get(ctx context.Context, agentID, userID, workspaceID string) (Agent, error) {
	row := repository.db.QueryRow(ctx, `
		SELECT `+columns+`
		FROM agents a
		LEFT JOIN users u ON u.id = a.owner_user_id
		WHERE a.id = $3 AND `+visibleSQL, userID, workspaceID, agentID)
	item, err := scan(row.Scan)
	if err != nil {
		return Agent{}, ErrNotFound
	}
	rows, err := repository.db.Query(ctx, `SELECT kb_id FROM agent_knowledge_bases WHERE agent_id = $1 ORDER BY created_at`, agentID)
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

// Update applies a partial change over what is stored. `current` is the agent
// the caller already loaded and was authorised against, so this never has to
// re-decide who may write.
func (repository *Repository) Update(ctx context.Context, current Agent, changes Changes) error {
	name := current.Name
	if changes.Name != nil {
		validated, err := ValidateName(*changes.Name)
		if err != nil {
			return err
		}
		name = validated
	}
	introduction := current.Introduction
	if changes.Introduction != nil {
		validated, err := ValidateIntroduction(*changes.Introduction)
		if err != nil {
			return err
		}
		introduction = validated
	}
	visibility := current.Visibility
	if changes.Visibility != nil && (*changes.Visibility == Private || *changes.Visibility == Shared) {
		visibility = *changes.Visibility
	}
	avatar := current.Avatar
	if changes.Avatar != nil {
		avatar = strings.TrimSpace(*changes.Avatar)
	}
	model := current.Model
	if changes.Model != nil {
		model = strings.TrimSpace(*changes.Model)
	}
	systemPrompt := current.SystemPrompt
	if changes.SystemPrompt != nil {
		systemPrompt = *changes.SystemPrompt
	}
	openingLine := current.OpeningLine
	if changes.OpeningLine != nil {
		openingLine = *changes.OpeningLine
	}
	tags := current.Tags
	if changes.Tags != nil {
		tags = CleanStringList(*changes.Tags, 10, 40)
	}
	presets := current.PresetQuestions
	if changes.PresetQuestions != nil {
		presets = CleanStringList(*changes.PresetQuestions, MaxPresetQuestions, 200)
	}
	hasSuggested := current.HasSuggestedQuestions
	if changes.HasSuggestedQuestions != nil {
		hasSuggested = *changes.HasSuggestedQuestions
	}
	isMemory := current.IsMemoryEnabled
	if changes.IsMemoryEnabled != nil {
		isMemory = *changes.IsMemoryEnabled
	}

	tagsJSON, _ := json.Marshal(tags)
	presetsJSON, _ := json.Marshal(presets)
	if _, err := repository.db.Exec(ctx, `
		UPDATE agents SET name = $2, introduction = $3, avatar = $4, tags = $5, visibility = $6,
			model = $7, system_prompt = $8, opening_line = $9, preset_questions = $10,
			has_suggested_questions = $11, is_memory_enabled = $12, updated_at = NOW()
		WHERE id = $1`,
		current.ID, name, introduction, avatar, tagsJSON, visibility, model, systemPrompt,
		openingLine, presetsJSON, hasSuggested, isMemory); err != nil {
		return err
	}
	if changes.KnowledgeBaseIDs != nil {
		return repository.SetKnowledgeBases(ctx, current.ID, current.WorkspaceID, *changes.KnowledgeBaseIDs)
	}
	return nil
}

func (repository *Repository) Delete(ctx context.Context, agentID string) error {
	_, err := repository.db.Exec(ctx, `DELETE FROM agents WHERE id = $1`, agentID)
	return err
}

// SetKnowledgeBases replaces the agent's reading list. A base has to be
// installed in the workspace before an agent there may read it: installing is
// the act that puts a base at the workspace's disposal, and an agent is one
// more thing in the workspace that draws on it. Merely being visible - a base
// published to everyone but not installed here - is not enough, or an agent
// would become a way to read a base the workspace never took on.
func (repository *Repository) SetKnowledgeBases(ctx context.Context, agentID, workspaceID string, kbIDs []string) error {
	wanted := CleanStringList(kbIDs, MaxKnowledgeBases, 64)
	if _, err := repository.db.Exec(ctx, `DELETE FROM agent_knowledge_bases WHERE agent_id = $1`, agentID); err != nil {
		return ErrKnowledgeSave
	}
	for _, kbID := range wanted {
		var installed bool
		if err := repository.db.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM knowledge_mounts km
				WHERE km.kb_id = $1 AND km.target_type = 'workspace' AND km.target_id = $2
			)`, kbID, workspaceID).Scan(&installed); err != nil || !installed {
			return ErrKnowledgeNotInstalled
		}
		if _, err := repository.db.Exec(ctx, `
			INSERT INTO agent_knowledge_bases(agent_id, kb_id) VALUES($1, $2)
			ON CONFLICT DO NOTHING`, agentID, kbID); err != nil {
			return ErrKnowledgeSave
		}
	}
	return nil
}

// Runtime reads the configuration a conversation runs on. It asks no
// visibility question on purpose: the right to use an agent was settled when
// the conversation was created, and a later change to who may see the agent
// must not silently strip an ongoing conversation of its instructions.
func (repository *Repository) Runtime(ctx context.Context, agentID string) (Runtime, error) {
	var item Runtime
	err := repository.db.QueryRow(ctx, `
		SELECT COALESCE(model, ''), COALESCE(system_prompt, ''), is_memory_enabled, has_suggested_questions
		FROM agents WHERE id = $1`, agentID).Scan(
		&item.Model, &item.SystemPrompt, &item.IsMemoryEnabled, &item.HasSuggestedQuestions)
	if err != nil {
		return Runtime{}, err
	}
	item.KnowledgeBaseIDs = []string{}
	rows, err := repository.db.Query(ctx, `SELECT kb_id FROM agent_knowledge_bases WHERE agent_id = $1`, agentID)
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

// Conversations and StartConversation write the same conversations table the
// general chat uses, stamped with agent_id; the general chat filters that
// column out, so the two surfaces share every message mechanism without ever
// sharing a list.
func (repository *Repository) Conversations(ctx context.Context, agentID, userID, workspaceID string) ([]Conversation, error) {
	rows, err := repository.db.Query(ctx, `
		SELECT id, workspace_id, title, created_at, updated_at
		FROM conversations
		WHERE user_id = $1 AND workspace_id = $2 AND agent_id = $3
		ORDER BY updated_at DESC LIMIT 100`, userID, workspaceID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Conversation{}
	for rows.Next() {
		var item Conversation
		if rows.Scan(&item.ID, &item.WorkspaceID, &item.Title, &item.CreatedAt, &item.UpdatedAt) == nil {
			items = append(items, item)
		}
	}
	return items, rows.Err()
}

func (repository *Repository) StartConversation(ctx context.Context, agentID, userID, workspaceID, title string) (Conversation, error) {
	conversationID := newID("cnv_")
	if _, err := repository.db.Exec(ctx, `
		INSERT INTO conversations(id, user_id, workspace_id, title, agent_id)
		VALUES($1, $2, $3, $4, $5)`, conversationID, userID, workspaceID, title, agentID); err != nil {
		return Conversation{}, err
	}
	return Conversation{ID: conversationID, WorkspaceID: workspaceID, Title: title}, nil
}

func (repository *Repository) Avatar(ctx context.Context, agentID string) ([]byte, string, error) {
	var image []byte
	var mime string
	err := repository.db.QueryRow(ctx, `SELECT avatar_image, COALESCE(avatar_mime, '') FROM agents WHERE id = $1`, agentID).Scan(&image, &mime)
	return image, mime, err
}

func (repository *Repository) SetAvatar(ctx context.Context, agentID string, image []byte, mime string) error {
	_, err := repository.db.Exec(ctx, `UPDATE agents SET avatar_image = $2, avatar_mime = $3, updated_at = NOW() WHERE id = $1`, agentID, image, mime)
	return err
}

func (repository *Repository) ClearAvatar(ctx context.Context, agentID string) error {
	_, err := repository.db.Exec(ctx, `UPDATE agents SET avatar_image = NULL, avatar_mime = NULL, updated_at = NOW() WHERE id = $1`, agentID)
	return err
}
