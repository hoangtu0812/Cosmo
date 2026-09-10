package httpapi

import (
	"context"
	"cosmo/backend/internal/modelgateway"
	"fmt"
	"net/http"
	"time"
)

func modelProgressContext(ctx context.Context, w http.ResponseWriter, flusher http.Flusher) context.Context {
	var last time.Time
	reasoning := ""
	return modelgateway.WithProgress(ctx, func(p modelgateway.Progress) {
		if p.Reasoning != "" {
			remaining := 16000 - len([]rune(reasoning))
			if remaining > 0 {
				r := []rune(p.Reasoning)
				if len(r) > remaining {
					r = r[:remaining]
				}
				reasoning += string(r)
			}
		}
		if !p.Done && time.Since(last) < 250*time.Millisecond {
			return
		}
		last = time.Now()
		if reasoning != "" {
			writeSSE(w, "status", map[string]string{"stage": "reasoning", "message": "Suy luận từ model", "detail": reasoning})
		}
		if p.Tool != "" {
			writeSSE(w, "status", map[string]string{"stage": "tool_preparing", "message": fmt.Sprintf("Đang soạn %s · %d byte", p.Tool, p.ArgumentBytes)})
		}
		flusher.Flush()
	})
}
