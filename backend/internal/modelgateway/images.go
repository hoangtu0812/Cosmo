package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const MaxImageResponseBytes = 12 << 20

type GeneratedImage struct {
	Base64 string `json:"b64_json"`
	URL    string `json:"url"`
}

// GenerateImage uses the workspace gateway but gives image jobs a longer deadline.
// No retries: a timed-out generation may already have been billed.
func (c *Client) GenerateImage(ctx context.Context, model, prompt, size string) (GeneratedImage, error) {
	if !c.HasGateway() {
		return GeneratedImage{}, ErrNotConfigured
	}
	body := map[string]any{"model": model, "prompt": prompt, "n": 1}
	if size != "" {
		body["size"] = size
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/images/generations", bytes.NewReader(payload))
	if err != nil {
		return GeneratedImage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	client := *c.httpClient
	client.Timeout = 5 * time.Minute
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return GeneratedImage{}, fmt.Errorf("không nhận được kết quả tạo ảnh; không tự thử lại: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return GeneratedImage{}, fmt.Errorf("gateway tạo ảnh trả HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxImageResponseBytes+1))
	if err != nil {
		return GeneratedImage{}, err
	}
	if len(raw) > MaxImageResponseBytes {
		return GeneratedImage{}, fmt.Errorf("ảnh trả về quá lớn")
	}
	var result struct {
		Data []GeneratedImage `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Data) == 0 {
		return GeneratedImage{}, fmt.Errorf("gateway không trả ảnh hợp lệ")
	}
	image := result.Data[0]
	if strings.TrimSpace(image.Base64) == "" && strings.TrimSpace(image.URL) == "" {
		return GeneratedImage{}, fmt.Errorf("gateway không trả nội dung ảnh")
	}
	return image, nil
}
