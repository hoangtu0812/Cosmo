// Package knowledge talks to the Knowledge Plane data service.
//
// The service parses, embeds and retrieves; it knows nothing about users or
// workspaces. Every call from here carries the access decision already made —
// an explicit list of knowledge base ids — so authorisation stays in the
// control plane and is applied before retrieval, never after it.
package knowledge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	ContentBase64   string `json:"content_base64,omitempty"`
	StorageKey      string `json:"storage_key,omitempty"`
	Title           string `json:"title"`
	DocumentVersion int    `json:"document_version"`
	EffectiveDate   string `json:"effective_date,omitempty"`
	LayoutMode      string `json:"layout_mode,omitempty"`
	EmbeddingModel  string `json:"embedding_model,omitempty"`
	RerankerModel   string `json:"reranker_model,omitempty"`
	ChunkSize       int    `json:"chunk_size,omitempty"`
	ChunkOverlap    int    `json:"chunk_overlap,omitempty"`
}

// ModelSettings selects the models used for an individual knowledge job. The
// control plane supplies it from the platform configuration, so the data
// service never has to hold database credentials or read deployment env vars.
type ModelSettings struct {
	EmbeddingModel string
	RerankerModel  string
	GatewayBaseURL string
	GatewayAPIKey  string
	RetrievalMode  string
	RerankEnabled  bool
	ScoreThreshold float64
	TopK           int
	ChunkSize      int
	ChunkOverlap   int
}

func (m ModelSettings) applyGatewayHeaders(request *http.Request) {
	request.Header.Set("X-Cosmo-Gateway-Base-URL", m.GatewayBaseURL)
	if m.GatewayAPIKey != "" {
		request.Header.Set("X-Cosmo-Gateway-API-Key", m.GatewayAPIKey)
	}
}

// IngestJob is one document to put through the pipeline.
//
// Content and StorageKey are the two ways to name the bytes, and a job carries
// exactly one of them. A new upload sends the bytes it has just received; a
// re-index sends the key of an original already in object storage, because
// reading it here only to encode it back to the service that stored it costs a
// full copy of every document in both directions.
type IngestJob struct {
	KBID        string
	DocumentID  string
	Filename    string
	ContentType string
	Title       string
	LayoutMode  string
	Version     int
	Content     []byte
	StorageKey  string
}

type IngestResult struct {
	DocumentID string `json:"document_id"`
	Chunks     int    `json:"chunks"`
	StorageKey string `json:"storage_key"`
}

// Event is one stage of an ingestion, as it happens.
type Event struct {
	Stage      string `json:"stage"`
	Message    string `json:"message"`
	Done       int    `json:"done"`
	Total      int    `json:"total"`
	Chunks     int    `json:"chunks"`
	StorageKey string `json:"storage_key"`
}

// Terminal reports whether this is the last event of a stream. A stream that
// ends without one was cut short rather than finished.
func (e Event) Terminal() bool { return e.Stage == "done" || e.Stage == "error" }

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
	// Matched names the retrievers that found this passage — dense, lexical or
	// both. It is what makes a hybrid index's second half auditable instead of
	// assumed.
	Matched []string `json:"matched"`
}

// DocumentChunk is one processed payload retained in Qdrant. Inspection is
// intentionally bounded by the service so opening an admin detail view cannot
// return an unbounded document through the control plane.
type DocumentChunk struct {
	ChunkIndex int    `json:"chunk_index"`
	Section    string `json:"section"`
	Page       string `json:"page"`
	Text       string `json:"text"`
}

// DocumentInspection describes the copy of a document currently indexed in
// Qdrant. The original bytes are retrieved separately from object storage.
type DocumentInspection struct {
	Indexed   bool            `json:"indexed"`
	Chunks    []DocumentChunk `json:"chunks"`
	Total     int             `json:"total"`
	Truncated bool            `json:"truncated"`
}

// Ingest sends one document through the pipeline, calling onEvent for each
// stage as it is reported.
//
// The service streams NDJSON because parsing and embedding a large manual
// takes minutes: waiting for a single response would leave the person who
// uploaded the file staring at nothing, unable to tell slow from stuck.
func (c *Client) Ingest(ctx context.Context, job IngestJob, models ModelSettings, onEvent func(Event)) (IngestResult, error) {
	body := IngestRequest{
		KBID:            job.KBID,
		DocumentID:      job.DocumentID,
		Filename:        job.Filename,
		ContentType:     job.ContentType,
		StorageKey:      job.StorageKey,
		Title:           job.Title,
		LayoutMode:      job.LayoutMode,
		DocumentVersion: job.Version,
		EmbeddingModel:  models.EmbeddingModel,
		RerankerModel:   models.RerankerModel,
		ChunkSize:       models.ChunkSize,
		ChunkOverlap:    models.ChunkOverlap,
	}
	if job.StorageKey == "" {
		body.ContentBase64 = base64.StdEncoding.EncodeToString(job.Content)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return IngestResult{}, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/ingest", bytes.NewReader(encoded))
	if err != nil {
		return IngestResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	models.applyGatewayHeaders(request)

	response, err := c.http.Do(request)
	if err != nil {
		return IngestResult{}, err
	}
	defer response.Body.Close()

	if response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return IngestResult{}, fmt.Errorf("knowledge service returned %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}

	scanner := bufio.NewScanner(response.Body)
	// A stage message carries a chunk of document text in the failure case, so
	// the line budget is well above the default 64 KB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var last Event
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		last = event
		if onEvent != nil {
			onEvent(event)
		}
	}
	if err := scanner.Err(); err != nil {
		return IngestResult{}, err
	}

	switch {
	case last.Stage == "error":
		return IngestResult{}, errors.New(last.Message)
	case last.Stage != "done":
		// The connection ended mid-pipeline. Reporting success here would mark
		// a half-indexed document as ready.
		return IngestResult{}, errors.New("ingestion ended before it finished")
	}
	return IngestResult{DocumentID: job.DocumentID, Chunks: last.Chunks, StorageKey: last.StorageKey}, nil
}

// Search retrieves passages across the knowledge bases the caller has already
// been authorised for. An empty list returns nothing and never widens to a
// search over everything.
func (c *Client) Search(ctx context.Context, query string, kbIDs []string, limit int, models ModelSettings) ([]Passage, error) {
	if len(kbIDs) == 0 || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	body := map[string]any{
		"query": query, "kb_ids": kbIDs,
		"embedding_model": models.EmbeddingModel, "reranker_model": models.RerankerModel,
		"retrieval_mode": models.RetrievalMode, "rerank_enabled": models.RerankEnabled,
		"score_threshold": models.ScoreThreshold,
	}
	if models.TopK > 0 {
		body["limit"] = models.TopK
	}
	if limit > 0 {
		body["limit"] = limit
	}
	var result struct {
		Results []Passage `json:"results"`
	}
	if err := c.call(ctx, http.MethodPost, "/search", body, &result, &models); err != nil {
		return nil, err
	}
	return result.Results, nil
}

func (c *Client) DeleteDocument(ctx context.Context, documentID, storageKey string) error {
	path := "/documents/" + url.PathEscape(documentID)
	if storageKey != "" {
		path += "?storage_key=" + url.QueryEscape(storageKey)
	}
	return c.call(ctx, http.MethodDelete, path, nil, nil, nil)
}

func (c *Client) InspectDocument(ctx context.Context, documentID string) (DocumentInspection, error) {
	var inspection DocumentInspection
	if err := c.call(ctx, http.MethodGet, "/documents/"+url.PathEscape(documentID)+"/inspection", nil, &inspection, nil); err != nil {
		return DocumentInspection{}, err
	}
	return inspection, nil
}

func (c *Client) OriginalDocument(ctx context.Context, documentID, storageKey string) ([]byte, error) {
	path := "/documents/" + url.PathEscape(documentID) + "/original?storage_key=" + url.QueryEscape(storageKey)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return nil, fmt.Errorf("knowledge service returned %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return io.ReadAll(response.Body)
}

func (c *Client) DeleteKnowledgeBase(ctx context.Context, kbID string) error {
	return c.call(ctx, http.MethodDelete, "/knowledge-bases/"+url.PathEscape(kbID), nil, nil, nil)
}

// ResetIndex removes only the derived Qdrant collection. Original documents
// remain in object storage so the control plane can ingest them again with the
// currently configured embedding model.
func (c *Client) ResetIndex(ctx context.Context) error {
	return c.call(ctx, http.MethodPost, "/collections/reset", nil, nil, nil)
}

func (c *Client) call(ctx context.Context, method, path string, body any, out any, models *ModelSettings) error {
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
	if models != nil {
		models.applyGatewayHeaders(request)
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
