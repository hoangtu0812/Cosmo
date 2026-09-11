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
	var pending modelgateway.Progress
	return modelgateway.WithProgress(ctx, func(p modelgateway.Progress) {
		if p.Tool != "" {
			pending = p
		}
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
		if pending.Tool != "" {
			detail := pending.Arguments
			// Keep live snapshots bounded; the completed call retains all arguments.
			runes := []rune(detail)
			if len(runes) > 24000 {
				detail = "…\n" + string(runes[len(runes)-24000:])
			}
			writeSSE(w, "status", map[string]string{"stage": "tool_preparing", "message": fmt.Sprintf("Đang soạn %s · %d byte", pending.Tool, pending.ArgumentBytes), "detail": detail})
		}
		flusher.Flush()
	})
}
