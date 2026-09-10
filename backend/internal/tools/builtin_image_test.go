package tools

import (
	"context"
	"cosmo/backend/internal/modelgateway"
	"encoding/base64"
	"strings"
	"testing"
)

func TestImageResult(t *testing.T) {
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="
	result, err := ImageResult(context.Background(), modelgateway.GeneratedImage{Base64: png}, "test")
	if err != nil || !strings.Contains(result, "data:image/png;base64,") {
		t.Fatalf("%s %v", result, err)
	}
	for _, image := range []modelgateway.GeneratedImage{
		{Base64: "invalid!"},
		{Base64: base64.StdEncoding.EncodeToString([]byte("<svg onload='alert(1)'/>"))},
		{URL: "http://127.0.0.1/private"},
		{URL: "file:///etc/passwd"},
	} {
		if _, err := ImageResult(context.Background(), image, "test"); err == nil {
			t.Fatal("accepted unsafe image")
		}
	}
}
