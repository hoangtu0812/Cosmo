package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Bound the serialized result too, since JSON escaping can expand HTML.
const MaxHTMLResultBytes = 200000

func renderHTML(arguments map[string]any) (string, error) {
	source, ok := arguments["html"].(string)
	if !ok || strings.TrimSpace(source) == "" {
		return "", fmt.Errorf("html phải là chuỗi HTML không rỗng")
	}
	if len(source) > MaxHTMLResultBytes {
		return "", fmt.Errorf("HTML vượt quá giới hạn %d byte", MaxHTMLResultBytes)
	}
	title := strings.TrimSpace(cleanString(arguments["title"]))
	if title == "" {
		title = "HTML"
	}
	payload, err := json.Marshal(map[string]any{"html": map[string]string{"title": title, "content": source}})
	if err != nil {
		return "", err
	}
	if len(payload) > MaxHTMLResultBytes {
		return "", fmt.Errorf("kết quả HTML vượt quá giới hạn %d byte", MaxHTMLResultBytes)
	}
	return string(payload), nil
}
