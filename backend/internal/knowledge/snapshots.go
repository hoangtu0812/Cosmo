package knowledge

import (
	"context"
	"net/http"
	"net/url"
)

type SnapshotResult struct {
	ID        string                      `json:"snapshot_id"`
	Chunks    int                         `json:"chunks"`
	Digest    string                      `json:"digest"`
	Originals map[string]SnapshotOriginal `json:"originals"`
}

type SnapshotOriginal struct {
	StorageKey  string `json:"storage_key"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	ETag        string `json:"etag,omitempty"`
}

func (c *Client) CreateSnapshot(ctx context.Context, id, kbID string, documents map[string]int, originals map[string]SnapshotOriginal, settings ModelSettings) (SnapshotResult, error) {
	var result SnapshotResult
	err := c.call(ctx, http.MethodPost, "/snapshots", map[string]any{"snapshot_id": id, "kb_id": kbID, "embedding_model": settings.EmbeddingModel, "documents": documents, "originals": originals}, &result, &settings)
	return result, err
}

func (c *Client) DiscardSnapshot(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, "/snapshots/"+url.PathEscape(id), nil, nil, nil)
}
