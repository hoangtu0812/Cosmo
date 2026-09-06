package knowledge

// Gateway observations contain no document, query, endpoint or credential.
type Observation struct {
	ID         string      `json:"id"`
	Model      string      `json:"model"`
	Phase      string      `json:"phase"`
	DurationMS int64       `json:"duration_ms"`
	Failed     bool        `json:"failed"`
	Usage      *TokenUsage `json:"usage"`
}

type TokenUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}
