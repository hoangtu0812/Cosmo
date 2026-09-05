package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/secrets"
)

const invitationTTL = 14 * 24 * time.Hour

// LLMSettings is the workspace-facing view of the model gateway credentials.
// The API key itself is never returned — only whether one is stored and the
// last four characters, so an operator can tell which key is in place.
type LLMSettings struct {
	BaseURL    string     `json:"base_url"`
	Model      string     `json:"model"`
	HasAPIKey  bool       `json:"has_api_key"`
	APIKeyHint string     `json:"api_key_hint,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
	Configured bool       `json:"configured"`
}

// Member is a person who can open a workspace.
type Member struct {
	UserID string    `json:"user_id"`
	Email  string    `json:"email"`
	Name   string    `json:"name"`
	Role   string    `json:"role"`
	Joined time.Time `json:"joined_at"`
}

// Invitation is a pending workspace invite. The raw token is returned exactly
// once, when the invite is created.
type Invitation struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	InviteURL string    `json:"invite_url,omitempty"`
}

// workspaceLLM reads the stored gateway settings for a workspace. It returns a
// zero value (not an error) when the workspace has never been configured.
func (s *Server) workspaceLLM(ctx context.Context, workspaceID string) (baseURL, model, apiKey, hint string, updatedAt *time.Time, err error) {
	var sealed []byte
	var updated time.Time
	row := s.db.QueryRow(ctx, `SELECT base_url, model, api_key_sealed, api_key_hint, updated_at FROM workspace_llm_configs WHERE workspace_id = $1`, workspaceID)
	if scanErr := row.Scan(&baseURL, &model, &sealed, &hint, &updated); scanErr != nil {
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return "", "", "", "", nil, nil
		}
		return "", "", "", "", nil, scanErr
	}
	if len(sealed) > 0 {
		apiKey, err = s.secrets.Open(sealed)
		if err != nil {
			// A key sealed under a previous SESSION_SECRET can no longer be
			// read. Report the row as keyless rather than failing the request,
			// so the workspace can simply re-enter it.
			s.logger.Warn("workspace api key could not be decrypted", "workspace_id", workspaceID, "error", err)
			apiKey = ""
		}
	}
	return baseURL, model, apiKey, hint, &updated, nil
}

// modelsFor builds the gateway client a workspace should use. Workspaces
// without their own settings fall back to the process-wide client from .env so
// existing installations keep working.
func (s *Server) modelsFor(ctx context.Context, workspaceID string) *modelgateway.Client {
	return s.modelsWith(ctx, workspaceID, "", "")
}

// modelsWith is modelsFor with an agent's own instructions and model put in
// front of the workspace defaults. Only the prompt and the model are the
// agent's to choose: the gateway address and key stay the workspace's, so an
// agent can never send its conversation somewhere else.
func (s *Server) modelsWith(ctx context.Context, workspaceID, systemPrompt, defaultModel string) *modelgateway.Client {
	baseURL, model, apiKey, _, _, err := s.workspaceLLM(ctx, workspaceID)
	if err != nil {
		s.logger.Error("read workspace model settings", "workspace_id", workspaceID, "error", err)
		return s.models
	}
	if baseURL == "" {
		return s.models
	}
	if strings.TrimSpace(defaultModel) != "" {
		model = defaultModel
	}
	prompt := s.cfg.LLMSystemPrompt
	if strings.TrimSpace(systemPrompt) != "" {
		prompt = systemPrompt
	}
	return modelgateway.New(baseURL, apiKey, model, prompt, s.cfg.LLMRequestTimeout)
}

// isWorkspaceAdmin reports whether the user may change workspace settings.
// Platform admins qualify everywhere; otherwise the membership role decides.
func (s *Server) isWorkspaceAdmin(ctx context.Context, user User, workspaceID string) bool {
	if user.Role == "admin" {
		return s.hasWorkspace(ctx, user.ID, workspaceID) || s.workspaceExists(ctx, workspaceID)
	}
	var role string
	if err := s.db.QueryRow(ctx, `SELECT role FROM workspace_memberships WHERE user_id = $1 AND workspace_id = $2`, user.ID, workspaceID).Scan(&role); err != nil {
		return false
	}
	return role == "owner" || role == "admin"
}

func (s *Server) workspaceExists(ctx context.Context, workspaceID string) bool {
	var exists bool
	return s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1)`, workspaceID).Scan(&exists) == nil && exists
}

// requireWorkspaceAdmin resolves the workspace ID from the URL and enforces
// admin rights, writing the error response itself when access is denied.
func (s *Server) requireWorkspaceAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	user := currentUser(r.Context())
	workspaceID := chi.URLParam(r, "workspaceID")
	if !s.isWorkspaceAdmin(r.Context(), user, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn cần quyền quản trị workspace để thực hiện thao tác này.")
		return "", false
	}
	return workspaceID, true
}

// ---------------------------------------------------------------- LLM config

func (s *Server) getLLMSettings(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := s.requireWorkspaceAdmin(w, r)
	if !ok {
		return
	}
	baseURL, model, apiKey, hint, updatedAt, err := s.workspaceLLM(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể đọc cấu hình model.")
		return
	}
	writeJSON(w, http.StatusOK, LLMSettings{
		BaseURL:    baseURL,
		Model:      model,
		HasAPIKey:  apiKey != "" || hint != "",
		APIKeyHint: hint,
		UpdatedAt:  updatedAt,
		Configured: baseURL != "",
	})
}

func (s *Server) putLLMSettings(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := s.requireWorkspaceAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		BaseURL string  `json:"base_url"`
		Model   string  `json:"model"`
		APIKey  *string `json:"api_key"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	input.Model = strings.TrimSpace(input.Model)
	if input.BaseURL != "" {
		parsed, err := url.Parse(input.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			writeError(w, http.StatusBadRequest, "Base URL phải là một địa chỉ http hoặc https hợp lệ.")
			return
		}
	}
	if len(input.Model) > 200 || len(input.BaseURL) > 500 {
		writeError(w, http.StatusBadRequest, "Base URL hoặc tên model quá dài.")
		return
	}

	user := currentUser(r.Context())
	previousURL, previousModel, _, _, _, _ := s.workspaceLLM(r.Context(), workspaceID)
	// The gateway address is where this workspace's conversations are sent, so
	// changing it is recorded with the address it was changed from. The key
	// never appears - only whether one was replaced or cleared.
	record := func(credential string) {
		s.audit(r, auditEvent{
			Action: "workspace.gateway.updated", TargetType: "workspace", TargetID: workspaceID,
			WorkspaceID: workspaceID,
			Metadata: map[string]string{
				"base_url": input.BaseURL, "model": input.Model, "api_key": credential,
				"previous_base_url": previousURL, "previous_model": previousModel,
			},
		})
	}
	// A nil api_key means "leave the stored key alone"; an empty string clears it.
	if input.APIKey == nil {
		_, err := s.db.Exec(r.Context(), `
			INSERT INTO workspace_llm_configs(workspace_id, base_url, model, updated_at, updated_by)
			VALUES($1, $2, $3, NOW(), $4)
			ON CONFLICT (workspace_id) DO UPDATE SET base_url = $2, model = $3, updated_at = NOW(), updated_by = $4`,
			workspaceID, input.BaseURL, input.Model, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể lưu cấu hình model.")
			return
		}
		record("unchanged")
		s.getLLMSettings(w, r)
		return
	}

	key := strings.TrimSpace(*input.APIKey)
	var sealed []byte
	hint := ""
	if key != "" {
		if !s.secrets.Configured() {
			writeError(w, http.StatusServiceUnavailable, "Máy chủ chưa có SESSION_SECRET nên không thể mã hoá API key.")
			return
		}
		var err error
		sealed, err = s.secrets.Seal(key)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể mã hoá API key.")
			return
		}
		hint = secrets.Hint(key)
	}
	_, err := s.db.Exec(r.Context(), `
		INSERT INTO workspace_llm_configs(workspace_id, base_url, model, api_key_sealed, api_key_hint, updated_at, updated_by)
		VALUES($1, $2, $3, $4, $5, NOW(), $6)
		ON CONFLICT (workspace_id) DO UPDATE SET base_url = $2, model = $3, api_key_sealed = $4, api_key_hint = $5, updated_at = NOW(), updated_by = $6`,
		workspaceID, input.BaseURL, input.Model, sealed, hint, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu cấu hình model.")
		return
	}
	if key == "" {
		record("cleared")
	} else {
		record("replaced")
	}
	s.getLLMSettings(w, r)
}

// listGatewayModels asks the workspace's gateway which models it serves, so the
// UI can offer a real list instead of a free-text field. It accepts a base URL
// and API key in the body so the settings screen can probe credentials the
// operator has typed but not yet saved — a POST body keeps the key out of URLs
// and access logs.
func (s *Server) listGatewayModels(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := s.requireWorkspaceAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSON(w, r, &input) {
			return
		}
	}
	baseURL, _, apiKey, _, _, err := s.workspaceLLM(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể đọc cấu hình model.")
		return
	}
	if candidate := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"); candidate != "" {
		baseURL = candidate
	}
	if typed := strings.TrimSpace(input.APIKey); typed != "" {
		apiKey = typed
	}
	if baseURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Chưa có Base URL.", "models": []string{}})
		return
	}

	models, probeErr := fetchGatewayModels(r.Context(), baseURL, apiKey)
	if probeErr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Không kết nối được tới gateway.", "models": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "models": models})
}

// listWorkspaceModels is the member-facing counterpart of listGatewayModels: it
// serves the model picker in the composer, so it takes no base URL or key
// overrides and never lets a non-admin probe an arbitrary host.
func (s *Server) listWorkspaceModels(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := chi.URLParam(r, "workspaceID")
	if !s.hasWorkspace(r.Context(), user.ID, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	baseURL, model, apiKey, _, _, err := s.workspaceLLM(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể đọc cấu hình model.")
		return
	}
	if baseURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{"models": []gatewayModel{}, "default": model})
		return
	}
	models, probeErr := fetchGatewayModels(r.Context(), baseURL, apiKey)
	if probeErr != nil {
		// The picker degrades to the workspace default rather than failing the
		// chat surface over an unreachable gateway.
		s.logger.Warn("list workspace models", "workspace_id", workspaceID, "error", probeErr)
		writeJSON(w, http.StatusOK, map[string]any{"models": []gatewayModel{}, "default": model})
		return
	}
	metadata := fetchGatewayModelMetadata(r.Context(), baseURL, apiKey)
	described := describeGatewayModels(models, metadata, true)
	writeJSON(w, http.StatusOK, map[string]any{"models": described, "default": model})
}

// listWorkspaceKnowledgeModels exposes the same saved workspace gateway to a
// Knowledge Base editor, enriched with model modes when the gateway reports
// them. No caller-supplied URL is accepted here: an editor can only inspect
// the gateway already approved for this workspace.
func (s *Server) listWorkspaceKnowledgeModels(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := s.requireWorkspaceAdmin(w, r)
	if !ok {
		return
	}
	baseURL, _, apiKey, _, _, err := s.workspaceLLM(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể đọc cấu hình model của workspace.")
		return
	}
	if baseURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"configured": false,
			"message":    "Workspace chưa cấu hình Model Gateway.",
			"models":     []gatewayModel{},
		})
		return
	}
	models, probeErr := fetchGatewayModels(r.Context(), baseURL, apiKey)
	if probeErr != nil {
		s.logger.Warn("list workspace knowledge models", "workspace_id", workspaceID, "error", probeErr)
		writeJSON(w, http.StatusOK, map[string]any{
			"configured": true,
			"message":    "Không kết nối được tới Model Gateway của workspace.",
			"models":     []gatewayModel{},
		})
		return
	}
	items := describeGatewayModels(models, fetchGatewayModelMetadata(r.Context(), baseURL, apiKey), false)
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "models": items})
}

// fetchGatewayModels asks an OpenAI-compatible gateway for its model list.
func fetchGatewayModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("gateway returned %d", response.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if item.ID != "" {
			models = append(models, item.ID)
		}
	}
	return models, nil
}

// gatewayModel pairs a model id with what the gateway says the model is for.
type gatewayModel struct {
	ID                     string   `json:"id"`
	Mode                   string   `json:"mode,omitempty"`
	Provider               string   `json:"provider,omitempty"`
	SupportsReasoning      bool     `json:"supports_reasoning"`
	ReasoningEfforts       []string `json:"reasoning_efforts,omitempty"`
	DefaultReasoningEffort string   `json:"default_reasoning_effort,omitempty"`
	// How much this model can be given at once, as the gateway reports it.
	// Zero where it does not say, which the reader is shown as a count with
	// nothing to compare against rather than as a made-up limit.
	ContextWindow int `json:"context_window,omitempty"`
}

type gatewayModelMetadata struct {
	Mode                   string
	Provider               string
	SupportsReasoning      bool
	ReasoningEfforts       []string
	DefaultReasoningEffort string
	ContextWindow          int
}

// fetchGatewayModelMetadata asks a LiteLLM-style gateway what every model can
// do. It is best effort: a plain OpenAI-compatible gateway has no /model/info,
// in which case chat models remain available but optional controls such as
// reasoning effort stay hidden rather than sending unsupported parameters.
func fetchGatewayModelMetadata(ctx context.Context, baseURL, apiKey string) map[string]gatewayModelMetadata {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/model/info", nil)
	if err != nil {
		return nil
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil
	}
	var payload struct {
		Data []struct {
			ModelName     string `json:"model_name"`
			LiteLLMParams struct {
				CustomProvider string `json:"custom_llm_provider"`
			} `json:"litellm_params"`
			ModelInfo struct {
				Mode                   string   `json:"mode"`
				MaxInputTokens         int      `json:"max_input_tokens"`
				MaxTokens              int      `json:"max_tokens"`
				Provider               string   `json:"litellm_provider"`
				SupportsReasoning      *bool    `json:"supports_reasoning"`
				ReasoningEffortLevels  []string `json:"reasoning_effort_levels"`
				DefaultReasoningEffort string   `json:"default_reasoning_effort"`
				SupportsNoneEffort     *bool    `json:"supports_none_reasoning_effort"`
				SupportsMinimalEffort  *bool    `json:"supports_minimal_reasoning_effort"`
				SupportsLowEffort      *bool    `json:"supports_low_reasoning_effort"`
				SupportsXHighEffort    *bool    `json:"supports_xhigh_reasoning_effort"`
				SupportsMaxEffort      *bool    `json:"supports_max_reasoning_effort"`
				SupportedOpenAIParams  []string `json:"supported_openai_params"`
			} `json:"model_info"`
		} `json:"data"`
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	metadata := make(map[string]gatewayModelMetadata, len(payload.Data))
	for _, item := range payload.Data {
		if item.ModelName == "" {
			continue
		}
		provider := item.ModelInfo.Provider
		if provider == "" {
			provider = item.LiteLLMParams.CustomProvider
		}
		efforts := reasoningEffortsFor(item.ModelInfo.SupportsReasoning, item.ModelInfo.ReasoningEffortLevels,
			item.ModelInfo.SupportedOpenAIParams, item.ModelInfo.SupportsNoneEffort, item.ModelInfo.SupportsMinimalEffort,
			item.ModelInfo.SupportsLowEffort, item.ModelInfo.SupportsXHighEffort, item.ModelInfo.SupportsMaxEffort)
		// max_input_tokens is the window a prompt has to fit in; max_tokens is
		// what some gateways call the same thing. Neither being present is
		// normal, and says only that this gateway does not know.
		window := item.ModelInfo.MaxInputTokens
		if window == 0 {
			window = item.ModelInfo.MaxTokens
		}
		metadata[item.ModelName] = gatewayModelMetadata{
			Mode: item.ModelInfo.Mode, Provider: provider, SupportsReasoning: len(efforts) > 0,
			ReasoningEfforts: efforts, DefaultReasoningEffort: item.ModelInfo.DefaultReasoningEffort,
			ContextWindow: window,
		}
	}
	return metadata
}

func describeGatewayModels(ids []string, metadata map[string]gatewayModelMetadata, chatOnly bool) []gatewayModel {
	items := make([]gatewayModel, 0, len(ids))
	for _, id := range ids {
		info := metadata[id]
		if chatOnly && !chatModelMode(info.Mode) {
			continue
		}
		provider := info.Provider
		if provider == "" {
			provider = providerFromModelID(id)
		}
		items = append(items, gatewayModel{
			ID: id, Mode: info.Mode, Provider: provider, SupportsReasoning: info.SupportsReasoning,
			ReasoningEfforts: info.ReasoningEfforts, DefaultReasoningEffort: info.DefaultReasoningEffort,
			ContextWindow: info.ContextWindow,
		})
	}
	return items
}

func chatModelMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "chat", "completion", "responses", "text":
		return true
	default:
		return false
	}
}

func providerFromModelID(id string) string {
	lower := strings.ToLower(id)
	providers := []struct {
		name    string
		markers []string
	}{
		{name: "azure", markers: []string{"azure/"}},
		{name: "openai", markers: []string{"openai/", "gpt-", "o1", "o3", "o4"}},
		{name: "anthropic", markers: []string{"anthropic/", "claude"}},
		{name: "google", markers: []string{"google/", "gemini"}},
		{name: "cohere", markers: []string{"cohere/", "command-", "rerank"}},
	}
	for _, provider := range providers {
		for _, marker := range provider.markers {
			if strings.Contains(lower, marker) {
				return provider.name
			}
		}
	}
	return ""
}

func reasoningEffortsFor(supportsReasoning *bool, explicit, supportedParams []string, none, minimal, low, xhigh, max *bool) []string {
	supported := supportsReasoning != nil && *supportsReasoning
	for _, parameter := range supportedParams {
		if parameter == "reasoning_effort" {
			supported = true
			break
		}
	}
	if !supported {
		return nil
	}
	allowed := map[string]bool{"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	if len(explicit) > 0 {
		result := make([]string, 0, len(explicit))
		for _, effort := range explicit {
			effort = strings.ToLower(strings.TrimSpace(effort))
			if allowed[effort] && !containsString(result, effort) {
				result = append(result, effort)
			}
		}
		return result
	}
	result := []string{"low", "medium", "high"}
	if low != nil && !*low {
		result = []string{"medium", "high"}
	}
	if minimal != nil && *minimal {
		result = append([]string{"minimal"}, result...)
	}
	if none != nil && *none {
		result = append([]string{"none"}, result...)
	}
	if xhigh != nil && *xhigh {
		result = append(result, "xhigh")
	}
	if max != nil && *max {
		result = append(result, "max")
	}
	return result
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------ workspace CRUD

func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 80 {
		writeError(w, http.StatusBadRequest, "Tên workspace phải từ 1 đến 80 ký tự.")
		return
	}
	input.Description = strings.TrimSpace(input.Description)
	if len([]rune(input.Description)) > 500 {
		writeError(w, http.StatusBadRequest, "Mô tả workspace không được quá 500 ký tự.")
		return
	}
	workspaceID := "ws_" + randomID(18)
	slug := slugify(input.Name) + "-" + strings.ToLower(randomID(4))

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo workspace.")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `INSERT INTO workspaces(id, name, slug, type, description) VALUES($1, $2, $3, 'team', $4)`, workspaceID, input.Name, slug, input.Description); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo workspace.")
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO workspace_memberships(user_id, workspace_id, role) VALUES($1, $2, 'owner')`, user.ID, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể gán quyền sở hữu workspace.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo workspace.")
		return
	}
	s.audit(r, auditEvent{
		Action: "workspace.created", TargetType: "workspace", TargetID: workspaceID, TargetLabel: input.Name,
		WorkspaceID: workspaceID, Metadata: map[string]string{"slug": slug, "type": "team"},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"workspace": Workspace{ID: workspaceID, Name: input.Name, Slug: slug, Type: "team", Description: input.Description, Role: "owner"}})
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := chi.URLParam(r, "workspaceID")
	if !s.hasWorkspace(r.Context(), user.ID, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT u.id, u.email, u.name, m.role, m.created_at
		FROM workspace_memberships m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = $1 ORDER BY m.created_at ASC`, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách thành viên.")
		return
	}
	defer rows.Close()
	members := []Member{}
	for rows.Next() {
		var item Member
		if rows.Scan(&item.UserID, &item.Email, &item.Name, &item.Role, &item.Joined) == nil {
			members = append(members, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// ------------------------------------------------------------- invitations

func (s *Server) listInvitations(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := s.requireWorkspaceAdmin(w, r)
	if !ok {
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT id, email, role, expires_at, created_at FROM workspace_invitations
		WHERE workspace_id = $1 AND accepted_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải lời mời.")
		return
	}
	defer rows.Close()
	invitations := []Invitation{}
	for rows.Next() {
		var item Invitation
		if rows.Scan(&item.ID, &item.Email, &item.Role, &item.ExpiresAt, &item.CreatedAt) == nil {
			invitations = append(invitations, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": invitations})
}

func (s *Server) createInvitation(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := s.requireWorkspaceAdmin(w, r)
	if !ok {
		return
	}
	if s.isPersonalWorkspace(r.Context(), workspaceID) {
		writeError(w, http.StatusBadRequest, "Không gian cá nhân chỉ dành cho một người. Hãy tạo workspace mới để làm việc cùng người khác.")
		return
	}
	var input struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if input.Email == "" || !strings.Contains(input.Email, "@") || len(input.Email) > 320 {
		writeError(w, http.StatusBadRequest, "Email không hợp lệ.")
		return
	}
	if input.Role != "admin" {
		input.Role = "member"
	}

	var alreadyMember bool
	_ = s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM workspace_memberships m JOIN users u ON u.id = m.user_id WHERE m.workspace_id = $1 AND u.email = $2)`, workspaceID, input.Email).Scan(&alreadyMember)
	if alreadyMember {
		writeError(w, http.StatusConflict, "Người này đã là thành viên của workspace.")
		return
	}

	// Only the hash is stored, so the link cannot be reconstructed from the database.
	token := randomID(24)
	sum := sha256.Sum256([]byte(token))
	user := currentUser(r.Context())
	invitation := Invitation{
		ID:        "inv_" + randomID(12),
		Email:     input.Email,
		Role:      input.Role,
		ExpiresAt: time.Now().Add(invitationTTL),
		CreatedAt: time.Now(),
	}
	_, err := s.db.Exec(r.Context(), `
		INSERT INTO workspace_invitations(id, workspace_id, email, role, token_hash, invited_by, expires_at)
		VALUES($1, $2, $3, $4, $5, $6, $7)`,
		invitation.ID, workspaceID, invitation.Email, invitation.Role, hex.EncodeToString(sum[:]), user.ID, invitation.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo lời mời.")
		return
	}
	// The invite email is recorded because inviting someone into a workspace is
	// granting them access to everything in it. The token is not: the row would
	// then be a working invitation link.
	s.audit(r, auditEvent{
		Action: "workspace.invitation.created", TargetType: "invitation", TargetID: invitation.ID,
		TargetLabel: invitation.Email, WorkspaceID: workspaceID,
		Metadata: map[string]string{"email": invitation.Email, "role": invitation.Role,
			"expires_at": invitation.ExpiresAt.UTC().Format(time.RFC3339)},
	})
	invitation.InviteURL = strings.TrimRight(s.cfg.FrontendURL, "/") + "/invite?token=" + token
	writeJSON(w, http.StatusCreated, map[string]any{"invitation": invitation})
}

func (s *Server) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := s.requireWorkspaceAdmin(w, r)
	if !ok {
		return
	}
	invitationID := chi.URLParam(r, "invitationID")
	// RETURNING rather than a separate read: the row is gone by the time the
	// audit is written, and an id with no email says nothing a year later.
	var email, role string
	err := s.db.QueryRow(r.Context(), `
		DELETE FROM workspace_invitations WHERE id = $1 AND workspace_id = $2 AND accepted_at IS NULL
		RETURNING email, role`, invitationID, workspaceID).Scan(&email, &role)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "Không thể thu hồi lời mời.")
		return
	}
	if err == nil {
		s.audit(r, auditEvent{
			Action: "workspace.invitation.revoked", TargetType: "invitation", TargetID: invitationID,
			TargetLabel: email, WorkspaceID: workspaceID,
			Metadata: map[string]string{"email": email, "role": role},
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// acceptInvitation joins the signed-in user to the workspace the token names.
// The invite email is not enforced against the account email: the link is the
// credential, and requiring a match would block anyone whose Entra address
// differs from the address the admin typed.
func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	var input struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Token = strings.TrimSpace(input.Token)
	if input.Token == "" {
		writeError(w, http.StatusBadRequest, "Thiếu mã lời mời.")
		return
	}
	sum := sha256.Sum256([]byte(input.Token))
	var invitationID, workspaceID, role string
	err := s.db.QueryRow(r.Context(), `
		SELECT id, workspace_id, role FROM workspace_invitations
		WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > NOW()`,
		hex.EncodeToString(sum[:])).Scan(&invitationID, &workspaceID, &role)
	if err != nil {
		writeError(w, http.StatusNotFound, "Lời mời không tồn tại hoặc đã hết hạn.")
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tham gia workspace.")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO workspace_memberships(user_id, workspace_id, role) VALUES($1, $2, $3)
		ON CONFLICT (user_id, workspace_id) DO NOTHING`, user.ID, workspaceID, role); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tham gia workspace.")
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE workspace_invitations SET accepted_at = NOW(), accepted_by = $2 WHERE id = $1`, invitationID, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể ghi nhận lời mời.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tham gia workspace.")
		return
	}

	var workspace Workspace
	_ = s.db.QueryRow(r.Context(), `SELECT id, name, slug, type FROM workspaces WHERE id = $1`, workspaceID).
		Scan(&workspace.ID, &workspace.Name, &workspace.Slug, &workspace.Type)
	workspace.Role = role
	s.audit(r, auditEvent{
		Action: "workspace.member.joined", TargetType: "user", TargetID: user.ID, TargetLabel: user.Email,
		WorkspaceID: workspaceID,
		Metadata:    map[string]string{"role": role, "invitation_id": invitationID},
	})
	writeJSON(w, http.StatusOK, map[string]any{"workspace": workspace})
}

// slugify reduces a workspace name to a URL-safe stem.
func slugify(name string) string {
	var b strings.Builder
	previousDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			previousDash = false
		default:
			if !previousDash && b.Len() > 0 {
				b.WriteByte('-')
				previousDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "workspace"
	}
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	return slug
}

// ---------------------------------------------------- conversation lifecycle

func (s *Server) renameConversation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	conversationID := chi.URLParam(r, "conversationID")
	if !s.ownsConversation(r.Context(), user.ID, conversationID) {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}
	var input struct {
		Title string `json:"title"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || len([]rune(input.Title)) > 200 {
		writeError(w, http.StatusBadRequest, "Tiêu đề phải từ 1 đến 200 ký tự.")
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE conversations SET title = $2, updated_at = NOW() WHERE id = $1`, conversationID, input.Title); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể đổi tên hội thoại.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": map[string]any{"id": conversationID, "title": input.Title}})
}

func (s *Server) deleteConversation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	conversationID := chi.URLParam(r, "conversationID")
	if !s.ownsConversation(r.Context(), user.ID, conversationID) {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}
	// messages.conversation_id cascades, so the transcript goes with it.
	// Deleting is the one thing a transcript cannot be asked about afterwards,
	// which is why it is recorded and creating one is not.
	var title, conversationWorkspaceID string
	var messageCount int
	_ = s.db.QueryRow(r.Context(), `
		SELECT c.title, c.workspace_id, (SELECT COUNT(*) FROM messages m WHERE m.conversation_id = c.id)
		FROM conversations c WHERE c.id = $1`, conversationID).Scan(&title, &conversationWorkspaceID, &messageCount)
	if _, err := s.db.Exec(r.Context(), `DELETE FROM conversations WHERE id = $1`, conversationID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá hội thoại.")
		return
	}
	s.audit(r, auditEvent{
		Action: "conversation.deleted", TargetType: "conversation", TargetID: conversationID, TargetLabel: title,
		WorkspaceID: conversationWorkspaceID, Metadata: map[string]int{"messages": messageCount},
	})
	w.WriteHeader(http.StatusNoContent)
}

// deleteMessage removes one turn from a transcript.
//
// A pair, not a message: deleting a question and leaving its answer produces a
// transcript that reads as the assistant volunteering something, and deleting
// an answer and leaving the question reads as it refusing. So a user message
// takes the assistant reply that followed it, and an assistant message takes
// the user message that prompted it.
//
// Ownership is checked through the conversation, which is the thing a reader
// owns; a message id on its own says nothing about who may touch it.
func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	conversationID := chi.URLParam(r, "conversationID")
	messageID := chi.URLParam(r, "messageID")
	if !s.ownsConversation(r.Context(), user.ID, conversationID) {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}

	deleted, role, err := s.deleteChatMessages(r.Context(), user.ID, conversationID, messageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Không tìm thấy tin nhắn.")
		} else if errors.Is(err, errChatTurnBusy) {
			writeError(w, http.StatusConflict, "Hội thoại đang trả lời. Vui lòng chờ trước khi xóa tin nhắn.")
		} else {
			writeError(w, http.StatusInternalServerError, "Không thể xoá tin nhắn.")
		}
		return
	}
	s.audit(r, auditEvent{
		Action: "conversation.message.deleted", TargetType: "conversation", TargetID: conversationID,
		WorkspaceID: s.conversationWorkspace(r.Context(), conversationID),
		Metadata:    map[string]any{"role": role, "deleted": deleted},
	})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

// conversationWorkspace is where a conversation lives, for the audit rows that
// are about a conversation but have to be filed under a workspace.
func (s *Server) conversationWorkspace(ctx context.Context, conversationID string) string {
	var workspaceID string
	_ = s.db.QueryRow(ctx, `SELECT workspace_id FROM conversations WHERE id = $1`, conversationID).Scan(&workspaceID)
	return workspaceID
}

// ------------------------------------------------------- workspace identity

// updateWorkspace changes the current workspace's display details. Every
// request field is optional: a nil pointer leaves the stored value alone.
func (s *Server) updateWorkspace(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := s.requireWorkspaceAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Icon        *string `json:"icon"`
		Context     *string `json:"context"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	// Name and description are yours to write either way; the icon is not,
	// because a personal workspace wears the account's own picture.
	if input.Icon != nil && s.isPersonalWorkspace(r.Context(), workspaceID) {
		writeError(w, http.StatusBadRequest, "Không gian cá nhân dùng ảnh đại diện của tài khoản.")
		return
	}
	if input.Context != nil {
		// Long enough to describe a department, short enough that prepending it
		// to every turn stays honest about what it costs.
		text := strings.TrimSpace(*input.Context)
		if len([]rune(text)) > 2000 {
			writeError(w, http.StatusBadRequest, "Bối cảnh workspace không được quá 2000 ký tự.")
			return
		}
		if _, err := s.db.Exec(r.Context(), `UPDATE workspaces SET context = $2 WHERE id = $1`, workspaceID, text); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể lưu bối cảnh workspace.")
			return
		}
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len([]rune(name)) > 80 {
			writeError(w, http.StatusBadRequest, "Tên workspace phải từ 1 đến 80 ký tự.")
			return
		}
		if _, err := s.db.Exec(r.Context(), `UPDATE workspaces SET name = $2 WHERE id = $1`, workspaceID, name); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể đổi tên workspace.")
			return
		}
	}
	if input.Description != nil {
		description := strings.TrimSpace(*input.Description)
		if len([]rune(description)) > 500 {
			writeError(w, http.StatusBadRequest, "Mô tả workspace không được quá 500 ký tự.")
			return
		}
		if _, err := s.db.Exec(r.Context(), `UPDATE workspaces SET description = $2 WHERE id = $1`, workspaceID, description); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể lưu mô tả workspace.")
			return
		}
	}
	if input.Icon != nil {
		icon := strings.TrimSpace(*input.Icon)
		// One glyph, which can still be several runes once modifiers and
		// zero-width joiners are counted (flags, skin tones, family emoji).
		if len([]rune(icon)) > 8 {
			writeError(w, http.StatusBadRequest, "Biểu tượng chỉ nhận một ký tự emoji.")
			return
		}
		if _, err := s.db.Exec(r.Context(), `UPDATE workspaces SET icon = $2 WHERE id = $1`, workspaceID, icon); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể lưu biểu tượng.")
			return
		}
	}

	var workspace Workspace
	err := s.db.QueryRow(r.Context(), `
		SELECT w.id, w.name, w.slug, w.type, COALESCE(w.description, ''), COALESCE(w.icon, ''), COALESCE(w.context, ''), (w.icon_image IS NOT NULL), m.role
		FROM workspaces w JOIN workspace_memberships m ON m.workspace_id = w.id AND m.user_id = $2
		WHERE w.id = $1`, workspaceID, currentUser(r.Context()).ID).
		Scan(&workspace.ID, &workspace.Name, &workspace.Slug, &workspace.Type, &workspace.Description, &workspace.Icon, &workspace.Context, &workspace.HasIconImage, &workspace.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể đọc lại workspace.")
		return
	}
	// Which fields the request actually carried, not which fields the workspace
	// has: every field is optional here, and a row saying "everything changed"
	// on a rename would be a lie.
	changed := []string{}
	for field, sent := range map[string]bool{
		"name": input.Name != nil, "description": input.Description != nil,
		"icon": input.Icon != nil, "context": input.Context != nil,
	} {
		if sent {
			changed = append(changed, field)
		}
	}
	sort.Strings(changed)
	if len(changed) > 0 {
		s.audit(r, auditEvent{
			Action: "workspace.updated", TargetType: "workspace", TargetID: workspaceID,
			TargetLabel: workspace.Name, WorkspaceID: workspaceID,
			Metadata: map[string]any{"fields": changed},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace": workspace})
}

// ------------------------------------------------------------ workspace icon

// allowedIconMIME limits uploads to formats a browser renders inline without
// scripting; SVG is excluded because it can carry script.
var allowedIconMIME = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

// maxIconBytes is generous for the 128px square the client sends after resizing.
const maxIconBytes = 256 * 1024

func (s *Server) workspaceIcon(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := chi.URLParam(r, "workspaceID")
	if !s.hasWorkspace(r.Context(), user.ID, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	var image []byte
	var mime string
	var kind string
	err := s.db.QueryRow(r.Context(),
		`SELECT icon_image, COALESCE(icon_mime, ''), type FROM workspaces WHERE id = $1`,
		workspaceID).Scan(&image, &mime, &kind)
	if err != nil {
		writeError(w, http.StatusNotFound, "Workspace chưa có ảnh biểu tượng.")
		return
	}
	// A personal workspace wears the face the account already has. Read through
	// rather than copied at sign-in, so it follows the account rather than
	// going stale the first time somebody changes their picture.
	if kind == "personal" && len(image) == 0 {
		err = s.db.QueryRow(r.Context(),
			`SELECT avatar_image, COALESCE(avatar_mime, '') FROM users WHERE id = $1`, user.ID).Scan(&image, &mime)
	}
	if err != nil || len(image) == 0 {
		writeError(w, http.StatusNotFound, "Workspace chưa có ảnh biểu tượng.")
		return
	}
	if !allowedIconMIME[mime] {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(image)
}

func (s *Server) uploadWorkspaceIcon(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := s.requireWorkspaceAdmin(w, r)
	if !ok {
		return
	}
	if s.isPersonalWorkspace(r.Context(), workspaceID) {
		writeError(w, http.StatusBadRequest, "Không gian cá nhân dùng ảnh đại diện của tài khoản.")
		return
	}
	var input struct {
		MIME string `json:"mime"`
		Data string `json:"data"` // base64, no data: prefix
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !allowedIconMIME[input.MIME] {
		writeError(w, http.StatusBadRequest, "Chỉ nhận ảnh PNG, JPEG, WebP hoặc GIF.")
		return
	}
	image, err := base64.StdEncoding.DecodeString(input.Data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Dữ liệu ảnh không hợp lệ.")
		return
	}
	if len(image) == 0 || len(image) > maxIconBytes {
		writeError(w, http.StatusBadRequest, "Ảnh phải nhỏ hơn 256 KB.")
		return
	}
	// Trust the bytes, not the declared type: a mismatch means the upload is
	// not what it claims to be.
	if sniffed := http.DetectContentType(image); sniffed != input.MIME {
		writeError(w, http.StatusBadRequest, "Nội dung ảnh không khớp định dạng khai báo.")
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE workspaces SET icon_image = $2, icon_mime = $3 WHERE id = $1`, workspaceID, image, input.MIME); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu ảnh biểu tượng.")
		return
	}
	s.audit(r, auditEvent{
		Action: "workspace.icon.updated", TargetType: "workspace", TargetID: workspaceID, WorkspaceID: workspaceID,
		Metadata: map[string]any{"mime": input.MIME, "bytes": len(image)},
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteWorkspaceIcon(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := s.requireWorkspaceAdmin(w, r)
	if !ok {
		return
	}
	if s.isPersonalWorkspace(r.Context(), workspaceID) {
		writeError(w, http.StatusBadRequest, "Không gian cá nhân dùng ảnh đại diện của tài khoản.")
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE workspaces SET icon_image = NULL, icon_mime = NULL WHERE id = $1`, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá ảnh biểu tượng.")
		return
	}
	s.audit(r, auditEvent{
		Action: "workspace.icon.removed", TargetType: "workspace", TargetID: workspaceID, WorkspaceID: workspaceID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// isPersonalWorkspace reports whether this is the workspace made for one
// person at sign-in. Three things follow from it - no invitations, no chosen
// icon, no deleting - so the question is asked in one place and the callers
// only state their own refusal.
func (s *Server) isPersonalWorkspace(ctx context.Context, workspaceID string) bool {
	var kind string
	if err := s.db.QueryRow(ctx, `SELECT type FROM workspaces WHERE id = $1`, workspaceID).Scan(&kind); err != nil {
		return false
	}
	return kind == "personal"
}

// deleteWorkspace removes a workspace and everything in it. Only its owner,
// and never a personal one: it is where a person lands at sign-in, and a
// member with none would have nowhere to stand.
func (s *Server) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := chi.URLParam(r, "workspaceID")
	var role string
	if err := s.db.QueryRow(r.Context(),
		`SELECT role FROM workspace_memberships WHERE user_id = $1 AND workspace_id = $2`,
		user.ID, workspaceID).Scan(&role); err != nil || role != "owner" {
		writeError(w, http.StatusForbidden, "Chỉ chủ workspace mới xoá được workspace.")
		return
	}
	if s.isPersonalWorkspace(r.Context(), workspaceID) {
		writeError(w, http.StatusBadRequest, "Không gian cá nhân không xoá được.")
		return
	}
	// Read the size of what is about to disappear while it still exists. This
	// is the one audit row that can never be reconstructed from the database
	// afterwards, because the database is what is being removed.
	var name string
	var members, conversations, knowledgeBases int
	_ = s.db.QueryRow(r.Context(), `
		SELECT w.name,
		       (SELECT COUNT(*) FROM workspace_memberships m WHERE m.workspace_id = w.id),
		       (SELECT COUNT(*) FROM conversations c WHERE c.workspace_id = w.id),
		       (SELECT COUNT(*) FROM knowledge_bases kb WHERE kb.owner_workspace_id = w.id)
		FROM workspaces w WHERE w.id = $1`, workspaceID).Scan(&name, &members, &conversations, &knowledgeBases)

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá workspace.")
		return
	}
	defer tx.Rollback(r.Context())
	// Everything the workspace owns goes with it through ON DELETE CASCADE.
	// last_workspace_id has no foreign key, so it is cleared here or a member
	// signs in pointing at a workspace that is gone.
	if _, err := tx.Exec(r.Context(),
		`UPDATE users SET last_workspace_id = NULL, updated_at = NOW() WHERE last_workspace_id = $1`, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá workspace.")
		return
	}
	if _, err := tx.Exec(r.Context(), `DELETE FROM workspaces WHERE id = $1`, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá workspace.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá workspace.")
		return
	}
	s.audit(r, auditEvent{
		Action: "workspace.deleted", TargetType: "workspace", TargetID: workspaceID, TargetLabel: name,
		WorkspaceID: workspaceID,
		Metadata: map[string]int{
			"members": members, "conversations": conversations, "knowledge_bases": knowledgeBases,
		},
	})
	w.WriteHeader(http.StatusNoContent)
}
