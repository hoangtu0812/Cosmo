// Package knowledge talks to the Knowledge Plane data service.
//
// The service parses, embeds and retrieves; it knows nothing about users or
// workspaces. Every call from here carries the access decision already made —
// an explicit list of knowledge base ids — so authorisation stays in the
// control plane and is applied before retrieval, never after it.
package knowledge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

// New returns nil when no service is configured, which callers read as "the
// knowledge plane is switched off" rather than as an error.
func New(baseURL string, timeout time.Duration) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: timeout}}
}

type IngestRequest struct {
	KBID            string `json:"kb_id"`
	DocumentID      string `json:"document_id"`
	Filename        string `json:"filename"`
	ContentType     string `json:"content_type"`
	ContentBase64   string `json:"content_base64"`
	Title           string `json:"title"`
	DocumentVersion int    `json:"document_version"`
	EffectiveDate   string `json:"effective_date,omitempty"`
}

type IngestResult struct {
	DocumentID string `json:"document_id"`
	Chunks     int    `json:"chunks"`
	StorageKey string `json:"storage_key"`
}

// Passage is one retrieved chunk, carrying enough provenance for the answer to
// be traced back to a document.
type Passage struct {
	KBID          string  `json:"kb_id"`
	DocumentID    string  `json:"document_id"`
	DocumentTitle string  `json:"document_title"`
	Source        string  `json:"source"`
	Section       string  `json:"section"`
	Page          string  `json:"page"`
	Text          string  `json:"text"`
	Score         float64 `json:"score"`
}

func (c *Client) Ingest(ctx context.Context, kbID, documentID, filename, contentType, title string, version int, content []byte) (IngestResult, error) {
	body := IngestRequest{
		KBID:            kbID,
		DocumentID:      documentID,
		Filename:        filename,
		ContentType:     contentType,
		ContentBase64:   base64.StdEncoding.EncodeToString(content),
		Title:           title,
		DocumentVersion: version,
	}
	var result IngestResult
	err := c.call(ctx, http.MethodPost, "/ingest", body, &result)
	return result, err
}

// Search retrieves passages across the knowledge bases the caller has already
// been authorised for. An empty list returns nothing and never widens to a
// search over everything.
func (c *Client) Search(ctx context.Context, query string, kbIDs []string, limit int) ([]Passage, error) {
	if len(kbIDs) == 0 || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	body := map[string]any{"query": query, "kb_ids": kbIDs}
	if limit > 0 {
		body["limit"] = limit
	}
	var result struct {
		Results []Passage `json:"results"`
	}
	if err := c.call(ctx, http.MethodPost, "/search", body, &result); err != nil {
		return nil, err
	}
	return result.Results, nil
}

func (c *Client) DeleteDocument(ctx context.Context, documentID, storageKey string) error {
	path := "/documents/" + url.PathEscape(documentID)
	if storageKey != "" {
		path += "?storage_key=" + url.QueryEscape(storageKey)
	}
	return c.call(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) DeleteKnowledgeBase(ctx context.Context, kbID string) error {
	return c.call(ctx, http.MethodDelete, "/knowledge-bases/"+url.PathEscape(kbID), nil, nil)
}

func (c *Client) call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("knowledge service returned %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}
