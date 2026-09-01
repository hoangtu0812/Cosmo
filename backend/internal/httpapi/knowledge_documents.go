package httpapi

import (
	"context"
	"encoding/json"

	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"cosmo/backend/internal/knowledge"
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

// KnowledgeDocumentDetail joins the control-plane metadata and processing log
// with the bounded Qdrant inspection returned by the knowledge service.
type KnowledgeDocumentDetail struct {
	Document   KnowledgeDocument            `json:"document"`
	Events     []DocumentEvent              `json:"events"`
	Inspection knowledge.DocumentInspection `json:"inspection"`
	IndexError string                       `json:"index_error,omitempty"`
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

func (s *Server) getKnowledgeDocumentDetail(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	documentID := chi.URLParam(r, "documentID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) == "" {
		writeError(w, http.StatusNotFound, "Không tìm thấy knowledge base.")
		return
	}

	document, _, err := s.knowledgeDocument(r.Context(), kbID, documentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy tài liệu.")
		return
	}
	events, err := s.documentEvents(r.Context(), documentID, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải nhật ký xử lý.")
		return
	}
	detail := KnowledgeDocumentDetail{Document: document, Events: events}
	if s.knowledge != nil && document.Status == "ready" {
		inspection, inspectionErr := s.knowledge.InspectDocument(r.Context(), documentID)
		if inspectionErr != nil {
			slog.Error("could not inspect knowledge document", "document", documentID, "error", inspectionErr)
			detail.IndexError = "Không thể đọc dữ liệu Qdrant của tài liệu."
		} else {
			detail.Inspection = inspection
		}
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) openKnowledgeDocumentOriginal(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	documentID := chi.URLParam(r, "documentID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) == "" {
		writeError(w, http.StatusNotFound, "Không tìm thấy knowledge base.")
		return
	}
	if s.knowledge == nil {
		writeError(w, http.StatusServiceUnavailable, "Dịch vụ tri thức chưa được cấu hình.")
		return
	}

	document, storageKey, err := s.knowledgeDocument(r.Context(), kbID, documentID)
	if err != nil || storageKey == "" {
		writeError(w, http.StatusNotFound, "Không tìm thấy bản gốc của tài liệu.")
		return
	}
	content, err := s.knowledge.OriginalDocument(r.Context(), documentID, storageKey)
	if err != nil {
		slog.Error("could not open knowledge document original", "document", documentID, "error", err)
		writeError(w, http.StatusBadGateway, "Không thể mở bản gốc của tài liệu.")
		return
	}
	w.Header().Set("Content-Type", document.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": document.Filename}))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
	_, _ = w.Write(content)
}

func (s *Server) knowledgeDocument(ctx context.Context, kbID, documentID string) (KnowledgeDocument, string, error) {
	var document KnowledgeDocument
	var storageKey string
	err := s.db.QueryRow(ctx, `
		SELECT id, kb_id, title, filename, content_type, size_bytes, version, status, chunk_count, error, created_at, updated_at, storage_key
		FROM knowledge_documents WHERE id = $1 AND kb_id = $2`, documentID, kbID).Scan(
		&document.ID, &document.KBID, &document.Title, &document.Filename, &document.ContentType,
		&document.SizeBytes, &document.Version, &document.Status, &document.ChunkCount, &document.Error,
		&document.CreatedAt, &document.UpdatedAt, &storageKey,
	)
	return document, storageKey, err
}

func (s *Server) uploadKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")

	// Adding to a knowledge base is a write, so viewers are turned away here
	// rather than at the ingestion service, which knows nothing about roles.
	if s.knowledgeAccess(r.Context(), user.ID, kbID) != "owner" {
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
	go s.ingestDocument(knowledge.IngestJob{
		KBID:        kbID,
		DocumentID:  documentID,
		Filename:    header.Filename,
		ContentType: header.Header.Get("Content-Type"),
		Title:       title,
		Version:     1,
		Content:     content,
	})

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

// ingestDocument sends one document through the knowledge service, recording
// each stage as it arrives and the outcome at the end.
//
// The stages are written to the database rather than only streamed, so the log
// survives a page reload and is readable by someone who was not watching while
// it ran. A failure is recorded on the row too, not merely logged, so the
// person who uploaded the file can see what became of it.
func (s *Server) ingestDocument(job knowledge.IngestJob) {
	documentID := job.DocumentID
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RAGTimeout)
	defer cancel()

	record := func(event knowledge.Event) {
		if _, err := s.db.Exec(context.Background(), `
			INSERT INTO knowledge_document_events(document_id, stage, message, done, total)
			VALUES($1, $2, $3, $4, $5)`,
			documentID, event.Stage, event.Message, event.Done, event.Total); err != nil {
			slog.Error("could not record ingestion event", "document", documentID, "error", err)
		}
	}

	record(knowledge.Event{Stage: "queued", Message: "Queued for ingestion"})

	// Read at ingestion time rather than passed in: both callers already know
	// the knowledge base, and looking it up here means a mode changed between
	// upload and re-index takes effect without a second plumbing route.
	if err := s.db.QueryRow(ctx, `SELECT layout_mode FROM knowledge_bases WHERE id = $1`, job.KBID).Scan(&job.LayoutMode); err != nil {
		slog.Error("could not read knowledge base layout mode", "kb", job.KBID, "error", err)
	}

	models, settingsErr := s.knowledgeModelSettings(ctx)
	if settingsErr != nil {
		slog.Error("could not load knowledge model settings", "document", documentID, "error", settingsErr)
	}
	result, err := s.knowledge.Ingest(ctx, job, models, record)
	if err != nil {
		slog.Error("document ingestion failed", "document", documentID, "error", err)
		message := ingestionErrorMessage(err)
		// The terminal event is as important as the row status: the browser's
		// live log only knows an ingestion finished when this line arrives.
		record(knowledge.Event{Stage: "error", Message: message})
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

// recoverInterruptedIngestions closes out documents left mid-flight by a
// restart, so the person who uploaded one sees what became of it instead of a
// spinner that never resolves.
func (s *Server) recoverInterruptedIngestions(ctx context.Context) error {
	const message = "Quá trình xử lý bị gián đoạn khi dịch vụ khởi động lại."
	tag, err := s.db.Exec(ctx, `
		UPDATE knowledge_documents SET status = 'failed', error = $1, updated_at = NOW()
		WHERE status IN ('pending', 'processing')`, message)
	if err != nil {
		return err
	}
	if count := tag.RowsAffected(); count > 0 {
		s.logger.Warn("marked interrupted ingestions as failed", "documents", count)
	}
	return nil
}

// ingestionErrorMessage keeps a useful, displayable cause in the document log
// without allowing an upstream stack trace or unbounded response body to take
// over the UI.
func ingestionErrorMessage(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 500 {
		return message[:500]
	}
	return message
}

// DocumentEvent is one line of an ingestion log.
type DocumentEvent struct {
	ID        int64     `json:"id"`
	Stage     string    `json:"stage"`
	Message   string    `json:"message"`
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	CreatedAt time.Time `json:"created_at"`
}

// eventPollInterval is how often a live log connection looks for new stages.
// Ingestion stages are seconds apart at best, so anything tighter would spend
// database round trips to shave latency nobody can perceive.
const eventPollInterval = 900 * time.Millisecond

// streamKnowledgeDocumentEvents sends the ingestion log as server-sent events.
//
// Everything recorded so far is replayed first, then the connection tails the
// document until it reaches a terminal stage. Opening the log late therefore
// shows the whole story rather than only what happens next.
func (s *Server) streamKnowledgeDocumentEvents(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	documentID := chi.URLParam(r, "documentID")

	if s.knowledgeAccess(r.Context(), user.ID, kbID) == "" {
		writeError(w, http.StatusNotFound, "Không tìm thấy knowledge base.")
		return
	}
	var exists bool
	if err := s.db.QueryRow(r.Context(),
		`SELECT EXISTS (SELECT 1 FROM knowledge_documents WHERE id = $1 AND kb_id = $2)`,
		documentID, kbID).Scan(&exists); err != nil || !exists {
		writeError(w, http.StatusNotFound, "Không tìm thấy tài liệu.")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming không được hỗ trợ.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	var lastID int64
	ticker := time.NewTicker(eventPollInterval)
	defer ticker.Stop()

	for {
		events, err := s.documentEvents(r.Context(), documentID, lastID)
		if err != nil {
			return
		}
		for _, event := range events {
			lastID = event.ID
			writeSSE(w, "stage", event)
			flusher.Flush()
			if event.Stage == "done" || event.Stage == "error" {
				return
			}
		}

		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// listKnowledgeDocumentEvents returns the log in one response, for readers that
// only want the history of a document that has already settled.
func (s *Server) listKnowledgeDocumentEvents(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), user.ID, kbID) == "" {
		writeError(w, http.StatusNotFound, "Không tìm thấy knowledge base.")
		return
	}
	events, err := s.documentEvents(r.Context(), chi.URLParam(r, "documentID"), 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải nhật ký xử lý.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) documentEvents(ctx context.Context, documentID string, afterID int64) ([]DocumentEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, stage, message, done, total, created_at
		FROM knowledge_document_events
		WHERE document_id = $1 AND id > $2
		ORDER BY id ASC`, documentID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]DocumentEvent, 0)
	for rows.Next() {
		var event DocumentEvent
		if err := rows.Scan(&event.ID, &event.Stage, &event.Message, &event.Done, &event.Total, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Server) deleteKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	kbID := chi.URLParam(r, "kbID")
	documentID := chi.URLParam(r, "documentID")

	if s.knowledgeAccess(r.Context(), user.ID, kbID) != "owner" {
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
func (s *Server) retrievalContext(ctx context.Context, workspaceID, query string) ([]knowledgePassage, error) {
	return s.retrievalContextFor(ctx, workspaceID, query, nil)
}

// retrievalContextFor is the same search narrowed to a chosen set of bases - an
// agent's reading list. Narrowing is all it can do: the workspace conditions
// below are applied first, so naming a base the workspace has not installed
// selects nothing rather than reaching outside the workspace. A non-nil but
// empty list therefore means "this agent reads no knowledge", which is
// different from nil, meaning "everything the workspace installed".
func (s *Server) retrievalContextFor(ctx context.Context, workspaceID, query string, only []string) ([]knowledgePassage, error) {
	if s.knowledge == nil || strings.TrimSpace(query) == "" {
		return nil, nil
	}

	// Two workspace-level conditions are required: the base is installed here
	// and this workspace is still within its reach. Both are workspace facts,
	// so a person who also belongs to the source workspace cannot accidentally
	// make its KB available in this one.
	rows, err := s.db.Query(ctx, `
		SELECT kb.id FROM knowledge_bases kb
		JOIN knowledge_mounts m ON m.kb_id = kb.id AND m.target_type = 'workspace' AND m.target_id = $1
		WHERE (`+workspaceRetrievableKnowledgeSQL+`)`, workspaceID)
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
	if only != nil {
		chosen := make(map[string]bool, len(only))
		for _, id := range only {
			chosen[id] = true
		}
		narrowed := make([]string, 0, len(kbIDs))
		for _, id := range kbIDs {
			if chosen[id] {
				narrowed = append(narrowed, id)
			}
		}
		kbIDs = narrowed
	}
	if len(kbIDs) == 0 {
		return nil, nil
	}

	models, settingsErr := s.knowledgeModelSettings(ctx)
	if settingsErr != nil {
		return nil, settingsErr
	}
	passages, err := s.knowledge.Search(ctx, query, kbIDs, 0, models)
	if err != nil {
		return nil, err
	}
	s.logRetrieval(ctx, workspaceID, query, kbIDs, passages)

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
			KBID:       passage.KBID,
			DocumentID: passage.DocumentID,
			Title:      passage.DocumentTitle,
			Source:     passage.Source,
			Section:    passage.Section,
			Page:       passage.Page,
			Text:       passage.Text,
		})
	}
	return result, nil
}

// logRetrieval records what was asked and what came back.
//
// This is the raw material a curated evaluation set is built from: the
// questions people actually ask, and which of them the index answered badly.
// It is off unless an operator turns it on, because it stores what someone
// typed, and it is deliberately best-effort — a chat answer must not fail
// because a measurement row could not be written.
func (s *Server) logRetrieval(ctx context.Context, workspaceID, query string, kbIDs []string, passages []knowledge.Passage) {
	if !s.cfg.RetrievalLog {
		return
	}
	type found struct {
		DocumentID string   `json:"document_id"`
		KBID       string   `json:"kb_id"`
		Score      float64  `json:"score"`
		Matched    []string `json:"matched"`
	}
	rows := make([]found, 0, len(passages))
	for _, passage := range passages {
		rows = append(rows, found{
			DocumentID: passage.DocumentID,
			KBID:       passage.KBID,
			Score:      passage.Score,
			Matched:    passage.Matched,
		})
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO knowledge_retrieval_log(workspace_id, query, kb_ids, passages)
		VALUES($1, $2, $3, $4::jsonb)`, workspaceID, query, kbIDs, string(encoded)); err != nil {
		slog.Warn("could not record retrieval", "workspace", workspaceID, "error", err)
	}
}

type knowledgePassage struct {
	KBID       string
	DocumentID string
	Title      string
	Source     string
	Section    string
	Page       string
	Text       string
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
