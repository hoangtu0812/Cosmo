package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cosmo/backend/internal/knowledge"
	"github.com/jackc/pgx/v5"
)

var errSnapshotChanged = errors.New("Knowledge Base đã thay đổi hoặc còn tài liệu chưa sẵn sàng; hãy publish lại sau khi xử lý xong.")

type snapshotReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// One database statement gives a consistent manifest. Revision timestamps cover
// ingestion, deletions, KB settings and gateway changes during the Qdrant copy.
func snapshotManifest(ctx context.Context, db snapshotReader, kbID string) (string, map[string]int, error) {
	var raw string
	err := db.QueryRow(ctx, `SELECT jsonb_build_object('kb_updated',kb.updated_at,'version',kb.version,
		'gateway_updated',w.updated_at,'documents',COALESCE((SELECT jsonb_agg(jsonb_build_object(
		'id',d.id,'version',d.version,'updated',d.updated_at,'status',d.status,'chunks',d.chunk_count,'storage_key',d.storage_key,'filename',d.filename,'content_type',d.content_type,'size_bytes',d.size_bytes) ORDER BY d.id)
		FROM knowledge_documents d WHERE d.kb_id=kb.id),'[]'::jsonb))::text
		FROM knowledge_bases kb LEFT JOIN workspace_llm_configs w ON w.workspace_id=kb.owner_workspace_id WHERE kb.id=$1`, kbID).Scan(&raw)
	if err != nil {
		return "", nil, err
	}
	var manifest struct {
		Documents []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Chunks int    `json:"chunks"`
		} `json:"documents"`
	}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return "", nil, err
	}
	if len(manifest.Documents) == 0 || len(manifest.Documents) > 10000 {
		return "", nil, errSnapshotChanged
	}
	documents := map[string]int{}
	for _, doc := range manifest.Documents {
		if doc.Status != "ready" || doc.Chunks <= 0 {
			return "", nil, errSnapshotChanged
		}
		documents[doc.ID] = doc.Chunks
	}
	return raw, documents, nil
}

func (s *Server) publishKnowledgeSnapshot(ctx context.Context, kbID string) (id string, version int, err error) {
	if s.knowledge == nil {
		return "", 0, fmt.Errorf("knowledge service is not configured")
	}
	before, documents, err := snapshotManifest(ctx, s.db, kbID)
	if err != nil {
		return "", 0, err
	}
	settings, err := s.knowledgeModelSettingsForKB(ctx, kbID)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.Sum256([]byte(randomID(32)))
	id = "kbs_" + hex.EncodeToString(hash[:16])
	// Cleanup is best effort after any failed/uncertain copy. Only this fresh ID
	// is eligible; never delete a previously published snapshot on a retry.
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// Resolve an uncertain commit before cleanup. If DB is unavailable,
			// retain the isolated collection for reconciliation instead of deleting.
			var exists bool
			if checkErr := s.db.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM knowledge_snapshots WHERE id=$1)`, id).Scan(&exists); checkErr == nil && !exists {
				if cleanupErr := s.knowledge.DiscardSnapshot(cleanup, id); cleanupErr != nil {
					s.logger.Warn("snapshot cleanup pending", "snapshot_id", id)
				}
			}
		}
	}()
	var manifest struct {
		Documents []struct {
			ID string `json:"id"`
			knowledge.SnapshotOriginal
		}
	}
	if err := json.Unmarshal([]byte(before), &manifest); err != nil {
		return id, 0, err
	}
	originals := map[string]knowledge.SnapshotOriginal{}
	for _, doc := range manifest.Documents {
		if doc.StorageKey == "" {
			return id, 0, errSnapshotChanged
		}
		originals[doc.ID] = doc.SnapshotOriginal
	}
	copy, err := s.knowledge.CreateSnapshot(ctx, id, kbID, documents, originals, settings)
	if err != nil {
		return id, 0, err
	}
	total := 0
	for _, count := range documents {
		total += count
	}
	_, digestErr := hex.DecodeString(copy.Digest)
	if copy.ID != id || copy.Chunks != total || len(copy.Digest) != 64 || digestErr != nil {
		return id, 0, fmt.Errorf("snapshot copy verification failed")
	}
	if len(copy.Originals) != len(originals) {
		return id, 0, fmt.Errorf("snapshot originals incomplete")
	}
	for doc, original := range originals {
		stored, ok := copy.Originals[doc]
		if !ok || stored.StorageKey != "knowledge-snapshots/"+id+"/"+url.PathEscape(doc) || stored.SizeBytes != original.SizeBytes || stored.Filename != original.Filename || stored.ContentType != original.ContentType {
			return id, 0, fmt.Errorf("snapshot original verification failed")
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return id, 0, err
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `SELECT version FROM knowledge_bases WHERE id=$1 FOR UPDATE`, kbID).Scan(&version); err != nil {
		return id, 0, err
	}
	after, _, err := snapshotManifest(ctx, tx, kbID)
	if err != nil || after != before {
		return id, 0, errSnapshotChanged
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return id, 0, err
	}
	version++
	encodedOriginals, _ := json.Marshal(copy.Originals)
	if _, err = tx.Exec(ctx, `INSERT INTO knowledge_snapshots(id,kb_id,version,manifest,model_settings,chunks,digest,originals) VALUES($1,$2,$3,$4::jsonb,$5::jsonb,$6,$7,$8::jsonb)`, id, kbID, version, before, string(encoded), copy.Chunks, copy.Digest, string(encodedOriginals)); err != nil {
		return id, 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE knowledge_bases SET version=$2,published_at=NOW(),updated_at=NOW() WHERE id=$1`, kbID, version); err != nil {
		return id, 0, err
	}
	err = tx.Commit(ctx)
	committed = err == nil
	return id, version, err
}

// A pin freezes retrieval configuration, not credentials or current access.
// Refuse endpoint/owner changes instead of sending a fresh key to an old host.
func (s *Server) snapshotModelSettings(ctx context.Context, kbID, id string) (knowledge.ModelSettings, error) {
	var raw []byte
	var owner string
	err := s.db.QueryRow(ctx, `SELECT ks.model_settings,kb.owner_workspace_id FROM knowledge_snapshots ks JOIN knowledge_bases kb ON kb.id=ks.kb_id WHERE ks.id=$1 AND ks.kb_id=$2`, id, kbID).Scan(&raw, &owner)
	if err != nil {
		return knowledge.ModelSettings{}, err
	}
	var settings knowledge.ModelSettings
	if err = json.Unmarshal(raw, &settings); err != nil {
		return settings, err
	}
	base, _, key, _, _, err := s.workspaceLLM(ctx, owner)
	if err != nil {
		return settings, err
	}
	if owner != settings.EmbeddingScope || strings.TrimRight(base, "/") != strings.TrimRight(settings.GatewayBaseURL, "/") {
		return settings, fmt.Errorf("snapshot gateway configuration changed")
	}
	settings.GatewayAPIKey = key
	settings.SnapshotID = id
	return settings, nil
}

func writeSnapshotError(w http.ResponseWriter, err error) {
	if errors.Is(err, errSnapshotChanged) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "Không thể tạo snapshot Knowledge Base. Chỉ mục đã publish được giữ nguyên.")
}
