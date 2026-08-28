package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// Visibility decides which workspaces may install a knowledge base. Everyone
// already belongs to a workspace, so a workspace is the unit of sharing;
// sharing with a person separately would only restate that.
const (
	// visibilityWorkspace keeps the base inside the workspace it was made in.
	visibilityWorkspace = "workspace"
	// visibilitySelected adds the workspaces named in knowledge_shares.
	visibilitySelected = "selected"
	// visibilityEveryone opens it to every workspace in the organisation.
	visibilityEveryone = "everyone"
)

// KnowledgeBase is a document collection owned by the user who created it.
// Ownership, reach and use are three separate questions, answered by
// owner_user_id, visibility, and knowledge_mounts respectively.
type KnowledgeBase struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	OwnerUserID      string    `json:"owner_user_id"`
	OwnerName        string    `json:"owner_name,omitempty"`
	OwnerWorkspaceID string    `json:"owner_workspace_id,omitempty"`
	Visibility       string    `json:"visibility"`
	CreatedAt        time.Time `json:"created_at"`
	// Access is what the caller may do: owner or viewer.
	Access string `json:"access"`

	// Version is what has been published. Zero means the base is still a draft
	// that only its owner can see.
	Version int `json:"version"`
	// HasUnpublishedChanges is true when documents changed after the last
	// publish, which is what turns the owner's Publish button back on.
	HasUnpublishedChanges bool `json:"has_unpublished_changes"`

	// The next three are only meaningful when the request named a workspace.
	IsMounted        bool `json:"is_mounted"`
	InstalledVersion int  `json:"installed_version"`
	// UpdateAvailable means the workspace installed an older version than the
	// one now published. Retrieval always uses the latest documents; this is
	// the owner telling installers that something changed.
	UpdateAvailable bool `json:"update_available"`

	DocumentCount int `json:"document_count"`
	SharedCount   int `json:"shared_count"`
}

// visibleKnowledgeSQL is the single definition of "knowledge bases this user
// may see", used by every read path so the rules cannot drift apart.
//
// A draft is visible only to its owner. Once published, the reach follows the
// visibility: always the workspace it belongs to, plus the named workspaces
// when selected, plus everyone when open.
const visibleKnowledgeSQL = `
	kb.owner_user_id = $1
	OR (kb.version > 0 AND (
		kb.visibility = 'everyone'
		OR EXISTS (
			SELECT 1 FROM workspace_memberships m
			WHERE m.workspace_id = kb.owner_workspace_id AND m.user_id = $1
		)
		OR (kb.visibility = 'selected' AND EXISTS (
			SELECT 1 FROM knowledge_shares sh
			JOIN workspace_memberships m ON m.workspace_id = sh.workspace_id AND m.user_id = $1
			WHERE sh.kb_id = kb.id
		))
	))`

// workspaceInScopeSQL is the same question asked of a workspace rather than a
// person: may this workspace install this base? Installing is what exposes a
// base to everyone in a workspace, so the check is on the workspace, not on
// the administrator who happens to press the button.
const workspaceInScopeSQL = `
	kb.version > 0 AND (
		kb.visibility = 'everyone'
		OR kb.owner_workspace_id = $2
		OR (kb.visibility = 'selected' AND EXISTS (
			SELECT 1 FROM knowledge_shares sh WHERE sh.kb_id = kb.id AND sh.workspace_id = $2
		))
	)`

// accessSQL resolves what the caller may do. Editing stays with the owner:
// sharing hands out use, never authorship.
const accessSQL = `CASE WHEN kb.owner_user_id = $1 THEN 'owner' ELSE 'viewer' END`

// unpublishedSQL reports whether documents changed since the last publish.
// Derived rather than stored, so it cannot fall out of step with reality.
const unpublishedSQL = `
	EXISTS (
		SELECT 1 FROM knowledge_documents d
		WHERE d.kb_id = kb.id AND d.updated_at > COALESCE(kb.published_at, TIMESTAMPTZ 'epoch')
	)`

// knowledgeAccess reports the caller's role, or an empty string when the
// knowledge base is not visible to them at all.
func (s *Server) knowledgeAccess(ctx context.Context, userID, kbID string) string {
	var access string
	err := s.db.QueryRow(ctx, `
		SELECT `+accessSQL+`
		FROM knowledge_bases kb
		WHERE kb.id = $2 AND (`+visibleKnowledgeSQL+`)`, userID, kbID).Scan(&access)
	if err != nil {
		return ""
	}
	return access
}

// knowledgeColumns is the shared projection. $1 is the caller, $2 the
// workspace the answer is framed against (empty string when none).
const knowledgeColumns = `
	kb.id, kb.name, kb.description, kb.owner_user_id, COALESCE(u.name, ''),
	COALESCE(kb.owner_workspace_id, ''), kb.visibility, kb.created_at, kb.version,
	` + accessSQL + `,
	` + unpublishedSQL + `,
	COALESCE((SELECT km.installed_version FROM knowledge_mounts km
	          WHERE km.kb_id = kb.id AND km.target_type = 'workspace' AND km.target_id = $2), -1),
	(SELECT COUNT(*) FROM knowledge_documents d WHERE d.kb_id = kb.id AND d.status = 'ready'),
	(SELECT COUNT(*) FROM knowledge_shares sh WHERE sh.kb_id = kb.id)`

func scanKnowledgeBase(scan func(...any) error) (KnowledgeBase, error) {
	var item KnowledgeBase
	// A mount that does not exist comes back as -1, which is how the query
	// distinguishes "not installed" from "installed at version 0".
	installed := -1
	err := scan(&item.ID, &item.Name, &item.Description, &item.OwnerUserID, &item.OwnerName,
		&item.OwnerWorkspaceID, &item.Visibility, &item.CreatedAt, &item.Version,
		&item.Access, &item.HasUnpublishedChanges, &installed,
		&item.DocumentCount, &item.SharedCount)
	if err != nil {
		return item, err
	}
	item.IsMounted = installed >= 0
	if item.IsMounted {
		item.InstalledVersion = installed
		item.UpdateAvailable = item.Version > installed
	}
	return item, nil
}

func (s *Server) listKnowledgeBases(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	// An optional workspace frames the answer: what is installed there, and
	// whether an update is waiting.
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace"))

	rows, err := s.db.Query(r.Context(), `
		SELECT `+knowledgeColumns+`
		FROM knowledge_bases kb
		LEFT JOIN users u ON u.id = kb.owner_user_id
		WHERE `+visibleKnowledgeSQL+`
		ORDER BY kb.created_at DESC`, user.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách knowledge base.")
		return
	}
	defer rows.Close()

	items := []KnowledgeBase{}
	for rows.Next() {
		if item, err := scanKnowledgeBase(rows.Scan); err == nil {
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"knowledge_bases": items})
}

func (s *Server) createKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		WorkspaceID string `json:"workspace_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 120 {
		writeError(w, http.StatusBadRequest, "Tên knowledge base phải từ 1 đến 120 ký tự.")
		return
	}
	if len([]rune(input.Description)) > 500 {
		writeError(w, http.StatusBadRequest, "Mô tả quá dài.")
		return
	}

	// A base belongs to the workspace it was made in, which is what the
	// narrowest visibility means. Falling back to the last workspace keeps the
	// call usable from anywhere in the app.
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if workspaceID == "" {
		workspaceID = user.LastWorkspaceID
	}
	if !s.hasWorkspace(r.Context(), user.ID, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn không thuộc workspace này.")
		return
	}

	kbID := "kb_" + randomID(18)
	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO knowledge_bases(id, name, description, owner_user_id, owner_workspace_id, visibility, version)
		VALUES($1, $2, $3, $4, $5, $6, 0)`,
		kbID, input.Name, strings.TrimSpace(input.Description), user.ID, workspaceID, visibilityWorkspace); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo knowledge base.")
		return
	}
	s.writeKnowledgeBase(w, r, kbID, http.StatusCreated)
}

func (s *Server) updateKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) != "owner" {
		writeError(w, http.StatusForbidden, "Chỉ chủ sở hữu mới sửa được knowledge base này.")
		return
	}
	var input struct {
		Name        *string   `json:"name"`
		Description *string   `json:"description"`
		Visibility  *string   `json:"visibility"`
		Workspaces  *[]string `json:"workspaces"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}

	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len([]rune(name)) > 120 {
			writeError(w, http.StatusBadRequest, "Tên knowledge base phải từ 1 đến 120 ký tự.")
			return
		}
		if _, err := s.db.Exec(r.Context(), `UPDATE knowledge_bases SET name = $2, updated_at = NOW() WHERE id = $1`, kbID, name); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể lưu thay đổi.")
			return
		}
	}
	if input.Description != nil {
		if len([]rune(*input.Description)) > 500 {
			writeError(w, http.StatusBadRequest, "Mô tả quá dài.")
			return
		}
		if _, err := s.db.Exec(r.Context(), `UPDATE knowledge_bases SET description = $2, updated_at = NOW() WHERE id = $1`, kbID, strings.TrimSpace(*input.Description)); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể lưu thay đổi.")
			return
		}
	}
	if input.Visibility != nil {
		switch *input.Visibility {
		case visibilityWorkspace, visibilitySelected, visibilityEveryone:
		default:
			writeError(w, http.StatusBadRequest, "Phạm vi chia sẻ không hợp lệ.")
			return
		}
		if err := s.setKnowledgeVisibility(r.Context(), kbID, *input.Visibility, input.Workspaces); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể lưu phạm vi chia sẻ.")
			return
		}
	} else if input.Workspaces != nil {
		if err := s.setKnowledgeShares(r.Context(), kbID, *input.Workspaces); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể lưu phạm vi chia sẻ.")
			return
		}
	}

	s.writeKnowledgeBase(w, r, kbID, http.StatusOK)
}

// setKnowledgeVisibility narrows or widens the reach, and drops any mount that
// the new reach no longer covers.
//
// Leaving those mounts in place would keep feeding a workspace's chat from a
// base it is no longer entitled to — visibility has to stay the single source
// of truth for what can be retrieved.
func (s *Server) setKnowledgeVisibility(ctx context.Context, kbID, visibility string, workspaces *[]string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `UPDATE knowledge_bases SET visibility = $2, updated_at = NOW() WHERE id = $1`, kbID, visibility); err != nil {
		return err
	}
	if visibility != visibilitySelected {
		if _, err := tx.Exec(ctx, `DELETE FROM knowledge_shares WHERE kb_id = $1`, kbID); err != nil {
			return err
		}
	} else if workspaces != nil {
		if err := replaceShares(ctx, tx, kbID, *workspaces); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM knowledge_mounts km
		USING knowledge_bases kb
		WHERE km.kb_id = kb.id AND kb.id = $1 AND km.target_type = 'workspace'
		  AND NOT (
			kb.visibility = 'everyone'
			OR kb.owner_workspace_id = km.target_id
			OR (kb.visibility = 'selected' AND EXISTS (
				SELECT 1 FROM knowledge_shares sh WHERE sh.kb_id = kb.id AND sh.workspace_id = km.target_id
			))
		  )`, kbID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Server) setKnowledgeShares(ctx context.Context, kbID string, workspaces []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := replaceShares(ctx, tx, kbID, workspaces); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// replaceShares makes the share list exactly what was asked for. Replacing
// rather than merging means unchecking a workspace in the UI actually removes
// it, instead of silently leaving the old reach in place.
func replaceShares(ctx context.Context, tx pgx.Tx, kbID string, workspaces []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM knowledge_shares WHERE kb_id = $1`, kbID); err != nil {
		return err
	}
	for _, workspaceID := range workspaces {
		workspaceID = strings.TrimSpace(workspaceID)
		if workspaceID == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO knowledge_shares(kb_id, workspace_id) VALUES($1, $2)
			ON CONFLICT (kb_id, workspace_id) DO NOTHING`, kbID, workspaceID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) deleteKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) != "owner" {
		writeError(w, http.StatusForbidden, "Chỉ chủ sở hữu mới xoá được knowledge base này.")
		return
	}
	// Shares and mounts cascade, so removing the base also detaches it
	// everywhere it was installed.
	if _, err := s.db.Exec(r.Context(), `DELETE FROM knowledge_bases WHERE id = $1`, kbID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá knowledge base.")
		return
	}
	if s.knowledge != nil {
		if err := s.knowledge.DeleteKnowledgeBase(r.Context(), kbID); err != nil {
			s.logger.Error("could not clear knowledge base from the index", "kb", kbID, "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// publishKnowledgeBase releases the current documents as the next version.
//
// Retrieval always reads the latest documents, so this does not change what a
// workspace gets back. What it changes is the announcement: installers see a
// new version and can acknowledge it, which is how an owner says "this is
// ready" rather than leaving people to guess.
func (s *Server) publishKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) != "owner" {
		writeError(w, http.StatusForbidden, "Chỉ chủ sở hữu mới publish được knowledge base này.")
		return
	}

	var ready int
	if err := s.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM knowledge_documents WHERE kb_id = $1 AND status = 'ready'`, kbID).Scan(&ready); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể publish knowledge base.")
		return
	}
	if ready == 0 {
		writeError(w, http.StatusBadRequest, "Cần ít nhất một tài liệu đã xử lý xong.")
		return
	}

	if _, err := s.db.Exec(r.Context(), `
		UPDATE knowledge_bases SET version = version + 1, published_at = NOW(), updated_at = NOW()
		WHERE id = $1`, kbID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể publish knowledge base.")
		return
	}
	s.writeKnowledgeBase(w, r, kbID, http.StatusOK)
}

func (s *Server) writeKnowledgeBase(w http.ResponseWriter, r *http.Request, kbID string, status int) {
	user := currentUser(r.Context())
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace"))
	item, err := scanKnowledgeBase(s.db.QueryRow(r.Context(), `
		SELECT `+knowledgeColumns+`
		FROM knowledge_bases kb LEFT JOIN users u ON u.id = kb.owner_user_id
		WHERE kb.id = $3`, user.ID, workspaceID, kbID).Scan)
	if err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy knowledge base.")
		return
	}
	writeJSON(w, status, map[string]any{"knowledge_base": item})
}

// ------------------------------------------------------------------- shares

func (s *Server) listKnowledgeShares(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) != "owner" {
		writeError(w, http.StatusForbidden, "Chỉ chủ sở hữu mới xem được danh sách chia sẻ.")
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT sh.workspace_id, COALESCE(w.name, '')
		FROM knowledge_shares sh LEFT JOIN workspaces w ON w.id = sh.workspace_id
		WHERE sh.kb_id = $1 ORDER BY w.name`, kbID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách chia sẻ.")
		return
	}
	defer rows.Close()

	type share struct {
		WorkspaceID string `json:"workspace_id"`
		Name        string `json:"name"`
	}
	shares := []share{}
	for rows.Next() {
		var item share
		if rows.Scan(&item.WorkspaceID, &item.Name) == nil {
			shares = append(shares, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": shares})
}

// ------------------------------------------------------------------- mounts

func (s *Server) listWorkspaceKnowledge(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := chi.URLParam(r, "workspaceID")
	if !s.hasWorkspace(r.Context(), user.ID, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	// Mounted *and* still in scope: a mount alone never keeps a base alive in
	// a workspace the owner has since shut out.
	rows, err := s.db.Query(r.Context(), `
		SELECT `+knowledgeColumns+`
		FROM knowledge_mounts km
		JOIN knowledge_bases kb ON kb.id = km.kb_id
		LEFT JOIN users u ON u.id = kb.owner_user_id
		WHERE km.target_type = 'workspace' AND km.target_id = $2
		  AND (`+visibleKnowledgeSQL+`) AND (`+workspaceInScopeSQL+`)
		ORDER BY kb.name`, user.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải knowledge base của workspace.")
		return
	}
	defer rows.Close()

	items := []KnowledgeBase{}
	for rows.Next() {
		if item, err := scanKnowledgeBase(rows.Scan); err == nil {
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"knowledge_bases": items})
}

// mountKnowledge installs a base into a workspace, or acknowledges a new
// version of one already installed.
//
// Publishing never installs anything by itself: reaching a workspace and being
// used by it stay separate decisions, and the second one belongs to that
// workspace's administrators.
func (s *Server) mountKnowledge(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := chi.URLParam(r, "workspaceID")
	kbID := chi.URLParam(r, "kbID")

	// Installing changes what every member of the workspace can retrieve, so
	// it is an admin action even though seeing the base is not.
	if !s.isWorkspaceAdmin(r.Context(), user, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn cần quyền quản trị workspace để cài knowledge base.")
		return
	}

	var version int
	err := s.db.QueryRow(r.Context(), `
		SELECT kb.version FROM knowledge_bases kb
		WHERE kb.id = $3 AND (`+visibleKnowledgeSQL+`) AND (`+workspaceInScopeSQL+`)`,
		user.ID, workspaceID, kbID).Scan(&version)
	if err != nil {
		writeError(w, http.StatusNotFound, "Knowledge base này chưa được chia sẻ tới workspace của bạn.")
		return
	}

	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO knowledge_mounts(kb_id, target_type, target_id, mounted_by, installed_version)
		VALUES($1, 'workspace', $2, $3, $4)
		ON CONFLICT (kb_id, target_type, target_id)
		DO UPDATE SET installed_version = $4`, kbID, workspaceID, user.ID, version); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể cài knowledge base.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unmountKnowledge(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := chi.URLParam(r, "workspaceID")
	kbID := chi.URLParam(r, "kbID")
	if !s.isWorkspaceAdmin(r.Context(), user, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn cần quyền quản trị workspace để gỡ knowledge base.")
		return
	}
	if _, err := s.db.Exec(r.Context(), `DELETE FROM knowledge_mounts WHERE kb_id = $1 AND target_type = 'workspace' AND target_id = $2`, kbID, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể gỡ knowledge base.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// workspaceDirectory lists every workspace by name, so an owner choosing who
// to share with can pick from the whole organisation rather than only the
// workspaces they happen to belong to. Names only — membership, conversations
// and settings stay behind their own checks.
func (s *Server) workspaceDirectory(w http.ResponseWriter, r *http.Request) {
	// Workspaces nobody belongs to cannot install anything, so listing them
	// would only offer share targets that can never act on the share.
	rows, err := s.db.Query(r.Context(), `
		SELECT w.id, w.name FROM workspaces w
		WHERE EXISTS (SELECT 1 FROM workspace_memberships m WHERE m.workspace_id = w.id)
		ORDER BY w.name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách workspace.")
		return
	}
	defer rows.Close()

	type entry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	items := []entry{}
	for rows.Next() {
		var item entry
		if rows.Scan(&item.ID, &item.Name) == nil {
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": items})
}
