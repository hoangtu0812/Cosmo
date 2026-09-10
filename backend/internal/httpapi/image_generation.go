package httpapi

import (
	"context"
	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/tools"
	"fmt"
	"sort"
	"strings"
)

func (s *Server) generateToolImage(ctx context.Context, caller tools.Caller, arguments map[string]any) (string, error) {
	prompt, _ := arguments["prompt"].(string)
	model, _ := arguments["model"].(string)
	size, _ := arguments["size"].(string)
	prompt = strings.TrimSpace(prompt)
	model = strings.TrimSpace(model)
	if prompt == "" || len(prompt) > 32000 {
		return "", fmt.Errorf("mô tả ảnh phải có từ 1 đến 32000 byte")
	}
	if len(model) > 200 || len(size) > 32 {
		return "", fmt.Errorf("model hoặc kích thước không hợp lệ")
	}
	base, _, key, _, _, err := s.workspaceLLM(ctx, caller.WorkspaceID)
	if err != nil {
		return "", fmt.Errorf("không đọc được cấu hình gateway của workspace")
	}
	if base == "" {
		base = s.cfg.LLMBaseURL
		key = s.cfg.LLMAPIKey
	}
	if base == "" {
		return "", fmt.Errorf("workspace chưa cấu hình gateway")
	}
	metadata := fetchGatewayModelMetadata(ctx, base, key)
	if model == "" {
		var candidates []string
		for id, info := range metadata {
			if info.Mode == "image_generation" {
				candidates = append(candidates, id)
			}
		}
		sort.Strings(candidates)
		if len(candidates) == 0 {
			return "", fmt.Errorf("gateway chưa công bố model image_generation; hãy chỉ định tên model tạo ảnh của gateway")
		}
		model = candidates[0]
	} else if info, ok := metadata[model]; ok && info.Mode != "" && info.Mode != "image_generation" {
		return "", fmt.Errorf("model %s không hỗ trợ tạo ảnh", model)
	}
	generated, err := modelgateway.New(base, key, model, "", s.cfg.LLMRequestTimeout).GenerateImage(ctx, model, prompt, size)
	if err != nil {
		return "", err
	}
	return tools.ImageResult(ctx, generated, "Ảnh đã tạo")
}
