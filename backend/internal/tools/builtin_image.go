package tools

import (
	"context"
	"cosmo/backend/internal/modelgateway"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Image results are bounded, self-contained raster images, never expiring URLs.
const MaxImageResultBytes = 12 << 20

func ImageResult(ctx context.Context, generated modelgateway.GeneratedImage, title string) (string, error) {
	var raw []byte
	var err error
	if generated.Base64 != "" {
		if len(generated.Base64) > MaxImageResultBytes {
			return "", fmt.Errorf("ảnh quá lớn")
		}
		raw, err = base64.StdEncoding.DecodeString(generated.Base64)
	} else {
		// Provider URLs are untrusted. Never forward gateway credentials to them.
		policy := EgressPolicy{}
		if err = policy.CheckEgress(generated.URL); err != nil {
			return "", err
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, generated.URL, nil)
		if e != nil {
			return "", e
		}
		resp, e := policy.guardedClient(nil).Do(req)
		if e != nil {
			return "", e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("không tải được ảnh: HTTP %d", resp.StatusCode)
		}
		raw, err = io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	}
	if err != nil {
		return "", fmt.Errorf("không đọc được dữ liệu ảnh")
	}
	if len(raw) > 8<<20 {
		return "", fmt.Errorf("ảnh vượt quá 8 MB")
	}
	mime := http.DetectContentType(raw)
	if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" && mime != "image/gif" {
		return "", fmt.Errorf("gateway phải trả ảnh PNG, JPEG, WebP hoặc GIF")
	}
	payload, err := json.Marshal(map[string]any{"image": map[string]string{"title": title, "src": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)}})
	return string(payload), err
}
