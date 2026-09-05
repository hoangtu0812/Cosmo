package knowledge

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
)

func (c *Client) StoreOriginal(ctx context.Context, documentID, contentType string, content []byte) error {
	var result struct {
		StorageKey string `json:"storage_key"`
		SizeBytes  int    `json:"size_bytes"`
	}
	err := c.call(ctx, http.MethodPost, "/originals", map[string]any{"document_id": documentID, "content_type": contentType, "content_base64": base64.StdEncoding.EncodeToString(content)}, &result, nil)
	if err == nil && (result.StorageKey != "knowledge-uploads/"+documentID || result.SizeBytes != len(content)) {
		return fmt.Errorf("original storage verification failed")
	}
	return err
}
