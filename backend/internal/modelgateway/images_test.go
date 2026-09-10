package modelgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGenerateImageRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("incorrect image request")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "image-model" || body["prompt"] != "a tree" || body["n"] != float64(1) {
			t.Error("incorrect image payload")
		}
		if _, ok := body["response_format"]; ok {
			t.Error("must not send unsupported response_format to GPT image models")
		}
		w.Write([]byte(`{"data":[{"b64_json":"aGVsbG8="}]}`))
	}))
	defer server.Close()
	got, err := New(server.URL, "test-key", "chat-model", "", time.Second).GenerateImage(context.Background(), "image-model", "a tree", "")
	if err != nil || got.Base64 != "aGVsbG8=" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestGenerateImageRejectsFailuresWithoutRetry(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":[{}]}`, `invalid`} {
		count := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count++; w.Write([]byte(body)) }))
		_, err := New(server.URL, "", "", "", time.Second).GenerateImage(context.Background(), "image", "test", "")
		server.Close()
		if err == nil || count != 1 {
			t.Fatalf("expected one failed request: %d, %v", count, err)
		}
	}
}
