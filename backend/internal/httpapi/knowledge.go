package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// KnowledgeBase is a document collection owned by the user who created it. It
// deliberately does not belong to a workspace: ownership, sharing and use are
// three separate questions, answered by owner_user_id, knowledge_grants and
// knowledge_mounts respectively.
type KnowledgeBase struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	OwnerUserID string    `json:"owner_user_id"`
	OwnerName   string    `json:"owner_name,omitempty"`
	Visibility  string    `json:"visibility"`
	CreatedAt   time.Time `json:"created_at"`
	// Access is what the caller may do: owner, editor or viewer.
	Access string `json:"access"`
	// IsMounted is only meaningful when the request named a workspace.
	IsMounted bool `json:"is_mounted"`
}

// KnowledgeGrant shares a knowledge base with a user or a whole workspace.
type KnowledgeGrant struct {
	SubjectType string    `json:"subject_type"`
	SubjectID   string    `json:"subject_id"`
	SubjectName string    `json:"subject_name,omitempty"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

// visibleKnowledgeSQL is the single definition of "knowledge bases this user
// may see". Every read path uses it so the rules cannot drift apart: the owner,
// anything published to the organization, plus direct and workspace grants.
const visibleKnowledgeSQL = `
	kb.owner_user_id = $1
	OR kb.visibility = 'organization'
	OR EXISTS (
		SELECT 1 FROM knowledge_grants g
		WHERE g.kb_id = kb.id AND g.subject_type = 'user' AND g.subject_id = $1
	)
	OR EXISTS (
		SELECT 1 FROM knowledge_grants g
		JOIN workspace_memberships m ON m.workspace_id = g.subject_id AND m.user_id = $1
		WHERE g.kb_id = kb.id AND g.subject_type = 'workspace'
	)`

// accessSQL resolves the caller's strongest role on a knowledge base.
const accessSQL = `
	CASE
		WHEN kb.owner_user_id = $1 THEN 'owner'
		WHEN EXISTS (
			SELECT 1 FROM knowledge_grants g
			WHERE g.kb_id = kb.id AND g.role = 'editor'
			  AND ((g.subject_type = 'user' AND g.subject_id = $1)
			    OR (g.subject_type = 'workspace' AND EXISTS (
					SELECT 1 FROM workspace_memberships m
					WHERE m.workspace_id = g.subject_id AND m.user_id = $1)))
		) THEN 'editor'
		ELSE 'viewer'
	END`

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

func (s *Server) listKnowledgeBases(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	// An optional workspace turns on the is_mounted flag, so the picker can show
	// what is already installed without a second round trip.
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace"))

	rows, err := s.db.Query(r.Context(), `
		SELECT kb.id, kb.name, kb.description, kb.owner_user_id, COALESCE(u.name, ''), kb.visibility, kb.created_at,
		       `+accessSQL+`,
		       EXISTS (SELECT 1 FROM knowledge_mounts km
		               WHERE km.kb_id = kb.id AND km.target_type = 'workspace' AND km.target_id = $2)
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
		var item KnowledgeBase
		if rows.Scan(&item.ID, &item.Name, &item.Description, &item.OwnerUserID, &item.OwnerName,
			&item.Visibility, &item.CreatedAt, &item.Access, &item.IsMounted) == nil {
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
	item := KnowledgeBase{
		ID:          "kb_" + randomID(18),
		Name:        input.Name,
		Description: strings.TrimSpace(input.Description),
		OwnerUserID: user.ID,
		OwnerName:   user.Name,
		Visibility:  "private",
		CreatedAt:   time.Now(),
		Access:      "owner",
	}
	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO knowledge_bases(id, name, description, owner_user_id, visibility)
		VALUES($1, $2, $3, $4, 'private')`, item.ID, item.Name, item.Description, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo knowledge base.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"knowledge_base": item})
}

func (s *Server) updateKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	access := s.knowledgeAccess(r.Context(), user.ID, kbID)
	if access != "owner" {
		writeError(w, http.StatusForbidden, "Chỉ chủ sở hữu mới sửa được knowledge base này.")
		return
	}
	var input struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Visibility  *string `json:"visibility"`
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
		if *input.Visibility != "private" && *input.Visibility != "organization" {
			writeError(w, http.StatusBadRequest, "Phạm vi chia sẻ không hợp lệ.")
			return
		}
		if _, err := s.db.Exec(r.Context(), `UPDATE knowledge_bases SET visibility = $2, updated_at = NOW() WHERE id = $1`, kbID, *input.Visibility); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể lưu thay đổi.")
			return
		}
	}
	s.writeKnowledgeBase(w, r, kbID)
}

func (s *Server) deleteKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) != "owner" {
		writeError(w, http.StatusForbidden, "Chỉ chủ sở hữu mới xoá được knowledge base này.")
		return
	}
	// Grants and mounts cascade, so removing the base also detaches it
	// everywhere it was installed.
	if _, err := s.db.Exec(r.Context(), `DELETE FROM knowledge_bases WHERE id = $1`, kbID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá knowledge base.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeKnowledgeBase(w http.ResponseWriter, r *http.Request, kbID string) {
	user := currentUser(r.Context())
	var item KnowledgeBase
	err := s.db.QueryRow(r.Context(), `
		SELECT kb.id, kb.name, kb.description, kb.owner_user_id, COALESCE(u.name, ''), kb.visibility, kb.created_at, `+accessSQL+`
		FROM knowledge_bases kb LEFT JOIN users u ON u.id = kb.owner_user_id
		WHERE kb.id = $2`, user.ID, kbID).
		Scan(&item.ID, &item.Name, &item.Description, &item.OwnerUserID, &item.OwnerName, &item.Visibility, &item.CreatedAt, &item.Access)
	if err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy knowledge base.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"knowledge_base": item})
}

// ------------------------------------------------------------------- grants

func (s *Server) listKnowledgeGrants(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) != "owner" {
		writeError(w, http.StatusForbidden, "Chỉ chủ sở hữu mới xem được danh sách chia sẻ.")
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT g.subject_type, g.subject_id, g.role, g.created_at,
		       COALESCE(u.email, w.name, '')
		FROM knowledge_grants g
		LEFT JOIN users u ON g.subject_type = 'user' AND u.id = g.subject_id
		LEFT JOIN workspaces w ON g.subject_type = 'workspace' AND w.id = g.subject_id
		WHERE g.kb_id = $1 ORDER BY g.created_at ASC`, kbID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách chia sẻ.")
		return
	}
	defer rows.Close()
	grants := []KnowledgeGrant{}
	for rows.Next() {
		var item KnowledgeGrant
		if rows.Scan(&item.SubjectType, &item.SubjectID, &item.Role, &item.CreatedAt, &item.SubjectName) == nil {
			grants = append(grants, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": grants})
}

func (s *Server) createKnowledgeGrant(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) != "owner" {
		writeError(w, http.StatusForbidden, "Chỉ chủ sở hữu mới chia sẻ được knowledge base này.")
		return
	}
	var input struct {
		SubjectType string `json:"subject_type"`
		Email       string `json:"email"`
		WorkspaceID string `json:"workspace_id"`
		Role        string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Role != "editor" {
		input.Role = "viewer"
	}

	var subjectID string
	switch input.SubjectType {
	case "user":
		email := strings.ToLower(strings.TrimSpace(input.Email))
		if err := s.db.QueryRow(r.Context(), `SELECT id FROM users WHERE email = $1`, email).Scan(&subjectID); err != nil {
			writeError(w, http.StatusNotFound, "Không tìm thấy người dùng với email này.")
			return
		}
		if subjectID == user.ID {
			writeError(w, http.StatusBadRequest, "Bạn đã là chủ sở hữu.")
			return
		}
	case "workspace":
		subjectID = strings.TrimSpace(input.WorkspaceID)
		// Sharing into a workspace the owner cannot see would let them probe for
		// workspace IDs, so require membership.
		if !s.hasWorkspace(r.Context(), user.ID, subjectID) {
			writeError(w, http.StatusForbidden, "Bạn không thuộc workspace này.")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "Đối tượng chia sẻ không hợp lệ.")
		return
	}

	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO knowledge_grants(kb_id, subject_type, subject_id, role) VALUES($1, $2, $3, $4)
		ON CONFLICT (kb_id, subject_type, subject_id) DO UPDATE SET role = $4`,
		kbID, input.SubjectType, subjectID, input.Role); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể chia sẻ knowledge base.")
		return
	}
	s.listKnowledgeGrants(w, r)
}

func (s *Server) deleteKnowledgeGrant(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) != "owner" {
		writeError(w, http.StatusForbidden, "Chỉ chủ sở hữu mới thu hồi được chia sẻ.")
		return
	}
	subjectType := chi.URLParam(r, "subjectType")
	subjectID := chi.URLParam(r, "subjectID")
	// Revoking access also detaches the base from that workspace, otherwise the
	// mount would keep feeding retrieval to people who can no longer see it.
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể thu hồi chia sẻ.")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `DELETE FROM knowledge_grants WHERE kb_id = $1 AND subject_type = $2 AND subject_id = $3`, kbID, subjectType, subjectID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể thu hồi chia sẻ.")
		return
	}
	if subjectType == "workspace" {
		if _, err := tx.Exec(r.Context(), `DELETE FROM knowledge_mounts WHERE kb_id = $1 AND target_type = 'workspace' AND target_id = $2`, kbID, subjectID); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể gỡ knowledge base khỏi workspace.")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể thu hồi chia sẻ.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------- mounts

func (s *Server) listWorkspaceKnowledge(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := chi.URLParam(r, "workspaceID")
	if !s.hasWorkspace(r.Context(), user.ID, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	// Mounted *and* still visible to the caller: a mount alone never grants
	// sight of a base whose share was revoked.
	rows, err := s.db.Query(r.Context(), `
		SELECT kb.id, kb.name, kb.description, kb.owner_user_id, COALESCE(u.name, ''), kb.visibility, kb.created_at, `+accessSQL+`
		FROM knowledge_mounts km
		JOIN knowledge_bases kb ON kb.id = km.kb_id
		LEFT JOIN users u ON u.id = kb.owner_user_id
		WHERE km.target_type = 'workspace' AND km.target_id = $2 AND (`+visibleKnowledgeSQL+`)
		ORDER BY kb.name`, user.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải knowledge base của workspace.")
		return
	}
	defer rows.Close()
	items := []KnowledgeBase{}
	for rows.Next() {
		var item KnowledgeBase
		if rows.Scan(&item.ID, &item.Name, &item.Description, &item.OwnerUserID, &item.OwnerName, &item.Visibility, &item.CreatedAt, &item.Access) == nil {
			item.IsMounted = true
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"knowledge_bases": items})
}

func (s *Server) mountKnowledge(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := chi.URLParam(r, "workspaceID")
	kbID := chi.URLParam(r, "kbID")
	// Mounting changes what every member of the workspace can retrieve, so it
	// is an admin action even though seeing the base is not.
	if !s.isWorkspaceAdmin(r.Context(), user, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn cần quyền quản trị workspace để cài knowledge base.")
		return
	}
	if s.knowledgeAccess(r.Context(), user.ID, kbID) == "" {
		writeError(w, http.StatusNotFound, "Không tìm thấy knowledge base.")
		return
	}
	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO knowledge_mounts(kb_id, target_type, target_id, mounted_by) VALUES($1, 'workspace', $2, $3)
		ON CONFLICT (kb_id, target_type, target_id) DO NOTHING`, kbID, workspaceID, user.ID); err != nil {
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
