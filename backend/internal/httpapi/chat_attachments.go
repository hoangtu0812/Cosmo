package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// Files attached to a question.
//
// A file handed over while asking about it is not knowledge: it is read once,
// answered about, and kept with the conversation so reopening it still shows
// what was being discussed. A document worth keeping belongs in a knowledge
// base, which is the other thing entirely - indexed, searchable, shared.
//
// So the text is stored and the original is not. That is the whole design: no
// object storage, no chunking, no embedding, nothing to clean up later.

const (
	// Twenty megabytes of source, and forty thousand characters of text out of
	// it. The first is what a person will actually attach; the second is what a
	// model can be handed without pushing the conversation out of its context.
	maxAttachmentBytes = 20 << 20
	maxAttachmentRunes = 40000
	// Four per message. Past that a person is describing a folder, and a
	// knowledge base is the right shape for a folder.
	maxAttachmentsPerTurn = 4
	// What every attachment in a conversation may cost a single turn, together.
	// A spreadsheet runs to tens of thousands of characters; three of them
	// would leave no room for the exchange they are being asked about.
	maxAttachmentContextRunes = 90000
)

// Attachment is a file read for one turn.
type Attachment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MIME        string `json:"mime"`
	ByteSize    int64  `json:"byte_size"`
	Chars       int    `json:"chars"`
	IsTruncated bool   `json:"is_truncated"`
	// Which question this file arrived with, empty while it is still waiting
	// for one, and when it was attached. Both only for the list, which is read
	// long after the turn that carried it.
	MessageID string    `json:"message_id,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// uploadAttachment reads one file and keeps its text against the conversation.
//
// The reading happens here rather than at send time so a reader learns straight
// away that a file could not be read, instead of asking a question and finding
// out afterwards that nothing was attached to it.
func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	conversationID := chi.URLParam(r, "conversationID")
	if !s.ownsConversation(r.Context(), user.ID, conversationID) {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}
	if s.knowledge == nil {
		writeError(w, http.StatusServiceUnavailable, "Dịch vụ đọc tệp chưa được cấu hình.")
		return
	}

	// Multipart, as knowledge documents already arrive: the shared JSON decoder
	// stops at a megabyte, which is smaller than any real document, and base64
	// would inflate what does fit by a third.
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "Tệp không hợp lệ hoặc vượt quá 20 MB.")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Thiếu tệp đính kèm.")
		return
	}
	defer file.Close()

	name := strings.TrimSpace(header.Filename)
	if name == "" || len([]rune(name)) > 200 {
		writeError(w, http.StatusBadRequest, "Tên tệp không hợp lệ.")
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Không đọc được tệp đính kèm.")
		return
	}
	if len(content) == 0 {
		writeError(w, http.StatusBadRequest, "Tệp rỗng.")
		return
	}
	if len(content) > maxAttachmentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Tệp tối đa 20 MB.")
		return
	}
	mime := header.Header.Get("Content-Type")

	var pending int
	if err := s.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM conversation_attachments WHERE conversation_id = $1 AND message_id IS NULL`,
		conversationID).Scan(&pending); err == nil && pending >= maxAttachmentsPerTurn {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Mỗi câu hỏi đính kèm tối đa %d tệp.", maxAttachmentsPerTurn))
		return
	}

	extracted, err := s.knowledge.ExtractText(r.Context(), name, mime, content)
	if err != nil {
		s.logger.Error("attachment extract failed", "conversation_id", conversationID, "name", name, "error", err)
		writeError(w, http.StatusUnprocessableEntity, "Không đọc được nội dung tệp này.")
		return
	}

	text := extracted.Text
	truncated := extracted.IsTruncated
	if runes := []rune(text); len(runes) > maxAttachmentRunes {
		text = string(runes[:maxAttachmentRunes])
		truncated = true
	}

	item := Attachment{
		ID:          "att_" + randomID(18),
		Name:        name,
		MIME:        mime,
		ByteSize:    int64(len(content)),
		Chars:       len([]rune(text)),
		IsTruncated: truncated,
	}
	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO conversation_attachments (id, conversation_id, user_id, name, mime, byte_size, text, is_truncated)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		item.ID, conversationID, user.ID, item.Name, item.MIME, item.ByteSize, text, item.IsTruncated); err != nil {
		writeError(w, http.StatusInternalServerError, "Không lưu được tệp đính kèm.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"attachment": item})
}

// deleteAttachment drops one before it has been asked about. After that it is
// part of a message, and removing it would leave an answer citing a file the
// transcript no longer shows.
func (s *Server) deleteAttachment(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	conversationID := chi.URLParam(r, "conversationID")
	if !s.ownsConversation(r.Context(), user.ID, conversationID) {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}
	if _, err := s.db.Exec(r.Context(),
		`DELETE FROM conversation_attachments
		 WHERE id = $1 AND conversation_id = $2 AND message_id IS NULL`,
		chi.URLParam(r, "attachmentID"), conversationID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không gỡ được tệp đính kèm.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pendingAttachments are the files attached but not yet asked about. The
// question about to be sent claims them, which is what stops the next question
// claiming them again.
func (s *Server) pendingAttachments(ctx context.Context, conversationID string) ([]attachmentText, error) {
	return s.readAttachments(ctx, `
		SELECT id, name, text, is_truncated FROM conversation_attachments
		WHERE conversation_id = $1 AND message_id IS NULL
		ORDER BY created_at`, conversationID)
}

// conversationAttachments is everything attached to this conversation, ever.
//
// A file does not stop existing because the question that carried it has been
// answered: "summarise this" and "now chart it" are two turns about one
// spreadsheet, and the second used to arrive with nothing but the first
// answer's prose.
//
// Newest first, and cut at a budget - the last file attached is the one being
// asked about, so it is the one that survives a conversation with more files
// than fit.
func (s *Server) conversationAttachments(ctx context.Context, conversationID string) ([]attachmentText, error) {
	return s.conversationAttachmentsFor(ctx, conversationID, nil)
}

func (s *Server) conversationAttachmentsFor(ctx context.Context, conversationID string, only []string) ([]attachmentText, error) {
	list, err := s.readAttachments(ctx, `
		SELECT id, name, text, is_truncated FROM conversation_attachments
		WHERE conversation_id = $1 AND ($2::text[] IS NULL OR id=ANY($2))
		ORDER BY created_at DESC`, conversationID, only)
	if err != nil {
		return nil, err
	}

	kept := make([]attachmentText, 0, len(list))
	budget := maxAttachmentContextRunes
	for _, item := range list {
		runes := []rune(item.Text)
		if len(runes) > budget {
			if budget < 2000 {
				// What is left is too little to be worth reading, and a
				// hundred characters of a spreadsheet is worse than none.
				break
			}
			item.Text = string(runes[:budget])
			item.IsTruncated = true
		}
		budget -= len([]rune(item.Text))
		kept = append(kept, item)
	}
	// Back into the order they were attached, which is the order they are
	// talked about.
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return kept, nil
}

func (s *Server) readAttachments(ctx context.Context, query string, args ...any) ([]attachmentText, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []attachmentText{}
	for rows.Next() {
		var item attachmentText
		if err := rows.Scan(&item.ID, &item.Name, &item.Text, &item.IsTruncated); err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, rows.Err()
}

type attachmentText struct {
	ID          string
	Name        string
	Text        string
	IsTruncated bool
}

// attachmentPrompt is how the files reach the model: named, in full, and said
// to be from the reader rather than from the documents, so an answer does not
// present an attachment as workspace knowledge.
func attachmentPrompt(files []attachmentText) string {
	if len(files) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Tệp người dùng đã đính kèm trong cuộc trò chuyện này. Đây là tệp của họ, không phải tài liệu nội bộ đã được lập chỉ mục. Dùng lại cho các câu hỏi sau, đừng yêu cầu gửi lại.\n")
	for _, file := range files {
		fmt.Fprintf(&builder, "\n--- %s ---\n%s\n", file.Name, file.Text)
		if file.IsTruncated {
			fmt.Fprintf(&builder, "(Tệp %s đã bị cắt bớt vì quá dài.)\n", file.Name)
		}
	}
	return builder.String()
}

// listAttachments is every file this conversation has been given, for the
// reader who wants to see what the model has been working from.
//
// Names and sizes, not text: the list is a list. Reading one is a second
// request, because a conversation with four spreadsheets in it would otherwise
// send a hundred thousand characters to draw a menu.
func (s *Server) listAttachments(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	conversationID := chi.URLParam(r, "conversationID")
	if !s.ownsConversation(r.Context(), user.ID, conversationID) {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT id, name, COALESCE(mime, ''), byte_size, LENGTH(text), is_truncated,
		       COALESCE(message_id, ''), created_at
		FROM conversation_attachments
		WHERE conversation_id = $1
		ORDER BY created_at`, conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không tải được danh sách tệp.")
		return
	}
	defer rows.Close()

	list := []Attachment{}
	for rows.Next() {
		var item Attachment
		if err := rows.Scan(&item.ID, &item.Name, &item.MIME, &item.ByteSize,
			&item.Chars, &item.IsTruncated, &item.MessageID, &item.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "Không tải được danh sách tệp.")
			return
		}
		list = append(list, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"attachments": list})
}

// readAttachment hands back the text the model was actually given.
//
// Not the original file: the original is not kept, and this is the honest
// answer to "what did it read" - including where the reading stopped, which a
// reader wondering why an answer missed the last sheet needs to see.
func (s *Server) readAttachment(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	conversationID := chi.URLParam(r, "conversationID")
	if !s.ownsConversation(r.Context(), user.ID, conversationID) {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}
	var item Attachment
	var text string
	if err := s.db.QueryRow(r.Context(), `
		SELECT id, name, COALESCE(mime, ''), byte_size, LENGTH(text), is_truncated,
		       COALESCE(message_id, ''), created_at, text
		FROM conversation_attachments
		WHERE id = $1 AND conversation_id = $2`,
		chi.URLParam(r, "attachmentID"), conversationID).Scan(
		&item.ID, &item.Name, &item.MIME, &item.ByteSize, &item.Chars,
		&item.IsTruncated, &item.MessageID, &item.CreatedAt, &text); err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy tệp đính kèm.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attachment": item, "text": text})
}
