package httpapi

import (
	"context"

	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// maxDocumentSize caps a single upload. Large manuals are the point of the
// feature, so the ceiling is generous, but it is a ceiling: an unbounded read
// would let one request exhaust the process.
const maxDocumentSize = 64 << 20 // 64 MB

// documentExtensions is what the ingestion service has readers for. Anything
// else is refused at the door rather than accepted and silently left unusable.
var documentExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".csv": true, ".json": true,
	".pdf": true, ".docx": true, ".pptx": true, ".html": true, ".htm": true,
}

// KnowledgeDocument is metadata and an object reference. The bytes live in
// object storage and the chunks in the vector store; Postgres holds neither.
type KnowledgeDocument struct {
	ID          string    `json:"id"`
	KBID        string    `json:"kb_id"`
	Title       string    `json:"title"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	Version     int       `json:"version"`
	Status      string    `json:"status"`
	ChunkCount  int       `json:"chunk_count"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Server) listKnowledgeDocuments(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) == "" {
		writeError(w, http.StatusNotFound, "Không tìm thấy knowledge base.")
		return
	}

	rows, err := s.db.Query(r.Context(), `
		SELECT id, kb_id, title, filename, content_type, size_bytes, version, status, chunk_count, error, created_at, updated_at
		FROM knowledge_documents WHERE kb_id = $1 ORDER BY created_at DESC`, kbID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách tài liệu.")
		return
	}
	defer rows.Close()

	documents := make([]KnowledgeDocument, 0)
	for rows.Next() {
		var item KnowledgeDocument
		if err := rows.Scan(&item.ID, &item.KBID, &item.Title, &item.Filename, &item.ContentType,
			&item.SizeBytes, &item.Version, &item.Status, &item.ChunkCount, &item.Error,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể đọc danh sách tài liệu.")
			return
		}
		documents = append(documents, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": documents})
}

func (s *Server) uploadKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")

	// Adding to a knowledge base is a write, so viewers are turned away here
	// rather than at the ingestion service, which knows nothing about roles.
	access := s.knowledgeAccess(r.Context(), user.ID, kbID)
	if access != "owner" && access != "editor" {
		writeError(w, http.StatusForbidden, "Bạn không có quyền thêm tài liệu vào knowledge base này.")
		return
	}
	if s.knowledge == nil {
		writeError(w, http.StatusServiceUnavailable, "Dịch vụ tri thức chưa được cấu hình.")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentSize+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Tệp tải lên không hợp lệ hoặc quá lớn.")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Thiếu tệp tải lên.")
		return
	}
	defer file.Close()

	extension := strings.ToLower(filepath.Ext(header.Filename))
	if !documentExtensions[extension] {
		writeError(w, http.StatusUnsupportedMediaType, "Định dạng tệp chưa được hỗ trợ.")
		return
	}

	content, err := io.ReadAll(io.LimitReader(file, maxDocumentSize+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Không đọc được tệp tải lên.")
		return
	}
	if len(content) == 0 {
		writeError(w, http.StatusBadRequest, "Tệp rỗng.")
		return
	}
	if int64(len(content)) > maxDocumentSize {
		writeError(w, http.StatusRequestEntityTooLarge, "Tệp vượt quá dung lượng cho phép.")
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = strings.TrimSuffix(header.Filename, extension)
	}

	documentID := "doc_" + randomID(18)
	_, err = s.db.Exec(r.Context(), `
		INSERT INTO knowledge_documents(id, kb_id, title, filename, content_type, size_bytes, status, uploaded_by)
		VALUES($1, $2, $3, $4, $5, $6, 'processing', $7)`,
		documentID, kbID, title, header.Filename, header.Header.Get("Content-Type"), len(content), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu tài liệu.")
		return
	}

	// Parsing and embedding a large manual takes minutes, far longer than a
	// browser will wait. The row is returned immediately as "processing" and
	// the ingestion runs on its own context so it survives the response.
	go s.ingestDocument(documentID, kbID, title, header.Filename, header.Header.Get("Content-Type"), content)

	writeJSON(w, http.StatusAccepted, map[string]any{"document": KnowledgeDocument{
		ID:          documentID,
		KBID:        kbID,
		Title:       title,
		Filename:    header.Filename,
		ContentType: header.Header.Get("Content-Type"),
		SizeBytes:   int64(len(content)),
		Version:     1,
		Status:      "processing",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}})
}

// ingestDocument sends one document through the knowledge service and records
// the outcome. A failure is written to the row rather than only logged, so the
// person who uploaded the file can see what happened to it.
func (s *Server) ingestDocument(documentID, kbID, title, filename, contentType string, content []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RAGTimeout)
	defer cancel()

	result, err := s.knowledge.Ingest(ctx, kbID, documentID, filename, contentType, title, 1, content)
	if err != nil {
		slog.Error("document ingestion failed", "document", documentID, "error", err)
		message := err.Error()
		if len(message) > 500 {
			message = message[:500]
		}
		if _, dbErr := s.db.Exec(context.Background(), `
			UPDATE knowledge_documents SET status = 'failed', error = $2, updated_at = NOW() WHERE id = $1`,
			documentID, message); dbErr != nil {
			slog.Error("could not record ingestion failure", "document", documentID, "error", dbErr)
		}
		return
	}

	if _, err := s.db.Exec(context.Background(), `
		UPDATE knowledge_documents
		SET status = 'ready', chunk_count = $2, storage_key = $3, error = '', updated_at = NOW()
		WHERE id = $1`, documentID, result.Chunks, result.StorageKey); err != nil {
		slog.Error("could not record ingestion result", "document", documentID, "error", err)
	}
}

func (s *Server) deleteKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	documentID := chi.URLParam(r, "documentID")

	access := s.knowledgeAccess(r.Context(), user.ID, kbID)
	if access != "owner" && access != "editor" {
		writeError(w, http.StatusForbidden, "Bạn không có quyền xoá tài liệu trong knowledge base này.")
		return
	}

	var storageKey string
	err := s.db.QueryRow(r.Context(), `
		SELECT storage_key FROM knowledge_documents WHERE id = $1 AND kb_id = $2`, documentID, kbID).Scan(&storageKey)
	if err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy tài liệu.")
		return
	}

	// Remove the chunks first. A row without chunks is a visible
	// inconsistency; chunks without a row are retrievable content nobody can
	// see or delete, which is worse.
	if s.knowledge != nil {
		if err := s.knowledge.DeleteDocument(r.Context(), documentID, storageKey); err != nil {
			slog.Error("could not remove document from knowledge service", "document", documentID, "error", err)
			writeError(w, http.StatusBadGateway, "Không thể xoá tài liệu khỏi dịch vụ tri thức.")
			return
		}
	}

	if _, err := s.db.Exec(r.Context(), `DELETE FROM knowledge_documents WHERE id = $1`, documentID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá tài liệu.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// retrievalContext resolves what the user may retrieve from in this workspace,
// then asks the knowledge service for passages.
//
// The allow-list is computed here, from mounts intersected with visibility, and
// passed explicitly. Retrieval never starts from "everything" and narrows
// afterwards: unauthorised chunks are not read, not scored and not logged.
func (s *Server) retrievalContext(ctx context.Context, userID, workspaceID, query string) ([]knowledgePassage, error) {
	if s.knowledge == nil || strings.TrimSpace(query) == "" {
		return nil, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT kb.id FROM knowledge_bases kb
		JOIN knowledge_mounts m ON m.kb_id = kb.id AND m.target_type = 'workspace' AND m.target_id = $2
		WHERE `+visibleKnowledgeSQL, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	kbIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		kbIDs = append(kbIDs, id)
	}
	if len(kbIDs) == 0 {
		return nil, nil
	}

	passages, err := s.knowledge.Search(ctx, query, kbIDs, 0)
	if err != nil {
		return nil, err
	}

	allowed := make(map[string]bool, len(kbIDs))
	for _, id := range kbIDs {
		allowed[id] = true
	}

	result := make([]knowledgePassage, 0, len(passages))
	for _, passage := range passages {
		// The service filtered on the same list, but this is the boundary
		// where a mistake would become a leak, so it is checked again.
		if !allowed[passage.KBID] {
			slog.Error("knowledge service returned an unauthorised passage", "kb", passage.KBID)
			continue
		}
		result = append(result, knowledgePassage{
			Title:   passage.DocumentTitle,
			Source:  passage.Source,
			Section: passage.Section,
			Page:    passage.Page,
			Text:    passage.Text,
		})
	}
	return result, nil
}

type knowledgePassage struct {
	Title   string
	Source  string
	Section string
	Page    string
	Text    string
}

// label names a passage the way a citation would, so the model can point at a
// source instead of asserting things anonymously.
func (p knowledgePassage) label() string {
	parts := []string{p.Title}
	if p.Section != "" {
		parts = append(parts, p.Section)
	}
	if p.Page != "" {
		parts = append(parts, "tr. "+p.Page)
	}
	return strings.Join(parts, " — ")
}

// buildGroundingPrompt turns retrieved passages into a system message.
//
// The instruction to answer only from the passages is what separates retrieval
// from decoration: without it the model treats them as suggestions and fills
// the gaps from memory.
func buildGroundingPrompt(passages []knowledgePassage) string {
	if len(passages) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Use the passages below to answer. Cite the source in square brackets after each claim that relies on it. If the passages do not contain the answer, say so instead of filling the gap.\n")
	for index, passage := range passages {
		fmt.Fprintf(&builder, "\n[%d] %s\n%s\n", index+1, passage.label(), passage.Text)
	}
	return builder.String()
}
