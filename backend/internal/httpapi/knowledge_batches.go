package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// One bounded request admits the complete batch before any remote storage.
// The existing worker publishes only after every document succeeds.
func (s *Server) uploadKnowledgeBatch(w http.ResponseWriter, r *http.Request) {
	kb, user := chi.URLParam(r, "kbID"), currentUser(r.Context())
	if s.knowledgeAccess(r.Context(), user.ID, kb) != "owner" {
		writeError(w, 403, "Bạn không có quyền thêm tài liệu.")
		return
	}
	if s.knowledge == nil {
		writeError(w, 503, "Dịch vụ tri thức chưa được cấu hình.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentSize+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, 400, "Lô tải lên không hợp lệ hoặc vượt 64 MB.")
		return
	}
	defer r.MultipartForm.RemoveAll()
	requestID := strings.TrimSpace(r.FormValue("request_id"))
	files := r.MultipartForm.File["files"]
	if requestID == "" || len(requestID) > 128 || len(files) == 0 || len(files) > 20 {
		writeError(w, 400, "Cần mã yêu cầu và từ 1 đến 20 tệp.")
		return
	}
	type upload struct {
		Filename, ContentType, Title, Digest string
		Content                              []byte `json:"-"`
	}
	uploads := make([]upload, 0, len(files))
	total := int64(0)
	for _, header := range files {
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if !documentExtensions[ext] {
			writeError(w, 415, "Định dạng tệp chưa được hỗ trợ.")
			return
		}
		file, err := header.Open()
		if err != nil {
			writeError(w, 400, "Không đọc được tệp.")
			return
		}
		content, err := io.ReadAll(io.LimitReader(file, maxDocumentSize+1))
		file.Close()
		total += int64(len(content))
		if err != nil || len(content) == 0 || total > maxDocumentSize {
			writeError(w, 400, "Tệp rỗng hoặc tổng dung lượng vượt 64 MB.")
			return
		}
		sum := sha256.Sum256(content)
		uploads = append(uploads, upload{header.Filename, header.Header.Get("Content-Type"), strings.TrimSuffix(header.Filename, ext), hex.EncodeToString(sum[:]), content})
	}
	raw, _ := json.Marshal(uploads)
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Không thể nhận lô tài liệu.")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(716042901)`); err != nil {
		writeError(w, 500, "Không thể khóa hàng đợi.")
		return
	}
	var locked string
	if err = tx.QueryRow(r.Context(), `SELECT id FROM knowledge_bases WHERE id=$1 FOR UPDATE`, kb).Scan(&locked); err != nil {
		writeError(w, 404, "Không tìm thấy KB.")
		return
	}
	var job, status, previous string
	var idsJSON []byte
	var ids []string
	err = tx.QueryRow(r.Context(), `SELECT id,status,request_hash,upload_document_ids FROM knowledge_ingestion_jobs WHERE kb_id=$1 AND requested_by=$2 AND request_id=$3`, kb, user.ID, requestID).Scan(&job, &status, &previous, &idsJSON)
	replay := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "Không đọc được lô tài liệu.")
		return
	}
	if replay {
		if previous != hash {
			writeError(w, 409, "Mã yêu cầu đã dùng cho một lô khác.")
			return
		}
		if json.Unmarshal(idsJSON, &ids) != nil || len(ids) != len(uploads) {
			writeError(w, 500, "Receipt không hợp lệ.")
			return
		}
	} else {
		for _, item := range uploads {
			id := "doc_" + randomID(18)
			ids = append(ids, id)
			if _, err = tx.Exec(r.Context(), `INSERT INTO knowledge_documents(id,kb_id,title,filename,content_type,size_bytes,status,uploaded_by,storage_key) VALUES($1,$2,$3,$4,$5,$6,'processing',$7,'knowledge-uploads/'||$1)`, id, kb, item.Title, item.Filename, item.ContentType, len(item.Content), user.ID); err != nil {
				writeError(w, 500, "Không lưu được tài liệu.")
				return
			}
		}
		job, err = enqueueIngestion(r.Context(), tx, kb, user.ID, "uploading")
		if err != nil {
			code := 500
			if errors.Is(err, errIngestionBusy) {
				code = 409
			}
			writeError(w, code, err.Error())
			return
		}
		idsJSON, _ = json.Marshal(ids)
		status = "uploading"
		if _, err = tx.Exec(r.Context(), `UPDATE knowledge_ingestion_jobs SET request_id=$2,request_hash=$3,upload_document_ids=$4 WHERE id=$1`, job, requestID, hash, idsJSON); err != nil {
			writeError(w, 500, "Không lưu được receipt.")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 503, "Chưa xác nhận nhận lô; thử lại cùng mã yêu cầu.")
		return
	}
	if status == "uploading" {
		for i, item := range uploads {
			if err = s.knowledge.StoreOriginal(r.Context(), ids[i], item.ContentType, item.Content); err != nil {
				writeJSON(w, 502, map[string]any{"error": "Chưa xác nhận đủ tệp gốc; thử lại cùng lô và mã yêu cầu.", "job_id": job, "document_ids": ids})
				return
			}
		}
		if _, err = s.db.Exec(r.Context(), `UPDATE knowledge_ingestion_jobs SET status='queued',next_attempt_at=NOW() WHERE id=$1 AND status='uploading'`, job); err != nil {
			writeError(w, 503, "Đã lưu tệp; hãy kiểm tra trạng thái lô.")
			return
		}
	}
	if !replay {
		workspace, name := s.knowledgeOwner(r.Context(), kb)
		s.audit(r, auditEvent{Action: "knowledge.batch.uploaded", TargetType: "knowledge_base", TargetID: kb, TargetLabel: name, WorkspaceID: workspace, Metadata: map[string]any{"job_id": job, "files": len(ids), "bytes": total}})
	}
	writeJSON(w, 202, map[string]any{"job_id": job, "document_ids": ids, "replayed": replay})
}

func (s *Server) listKnowledgeIngestionJobs(w http.ResponseWriter, r *http.Request) {
	kb := chi.URLParam(r, "kbID")
	if s.knowledgeAccess(r.Context(), currentUser(r.Context()).ID, kb) != "owner" {
		writeError(w, 403, "Bạn không có quyền xem tác vụ ingest.")
		return
	}
	var raw []byte
	err := s.db.QueryRow(r.Context(), `SELECT COALESCE(jsonb_agg(j ORDER BY j.created_at DESC),'[]') FROM (
 SELECT id,status,error_code,attempts,created_at,finished_at,jsonb_array_length(upload_document_ids) AS files,
 (SELECT count(*) FROM knowledge_ingestion_checkpoints c WHERE c.job_id=i.id) AS completed_documents,
 jsonb_array_length(COALESCE(manifest->'documents','[]')) AS total_documents
 FROM knowledge_ingestion_jobs i WHERE kb_id=$1 ORDER BY created_at DESC LIMIT 20) j`, kb).Scan(&raw)
	if err != nil {
		writeError(w, 500, "Không đọc được trạng thái ingest.")
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": json.RawMessage(raw)})
}
