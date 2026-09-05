package knowledge

import (
	"context"
	"net/http"
	"net/url"
)

type SnapshotResult struct {
	ID     string `json:"snapshot_id"`
	Chunks int    `json:"chunks"`
	Digest string `json:"digest"`
}

func (c *Client) CreateSnapshot(ctx context.Context, id, kbID string, documents map[string]int, settings ModelSettings) (SnapshotResult, error) {
	var result SnapshotResult
	err := c.call(ctx, http.MethodPost, "/snapshots", map[string]any{"snapshot_id": id, "kb_id": kbID, "embedding_model": settings.EmbeddingModel, "documents": documents}, &result, &settings)
	return result, err
}

func (c *Client) DiscardSnapshot(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, "/snapshots/"+url.PathEscape(id), nil, nil, nil)
}
