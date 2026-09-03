package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address             string
	DatabaseURL         string
	FrontendURL         string
	SessionSecret       string
	SessionTTL          time.Duration
	CookieSecure        bool
	AdminEmail          string
	PlatformAdminEmails map[string]bool
	AdminPassword       string
	// Hosts a tool may reach even though they resolve privately. Empty keeps
	// the default of public internet only.
	ToolEgressAllowedHosts []string
	AdminName              string
	EntraTenantID          string
	EntraClientID          string
	EntraClientSecret      string
	EntraRedirectURL       string
	LLMBaseURL             string
	LLMAPIKey              string
	LLMModel               string
	LLMSystemPrompt        string
	LLMRequestTimeout      time.Duration
	RAGServiceURL          string
	RAGTimeout             time.Duration
	// ReindexWorkers is how many documents a re-index rebuilds at once. Each
	// one occupies the knowledge service and the model gateway, so this is the
	// knob for how much of that capacity a rebuild is allowed to take.
	ReindexWorkers int
	// RetrievalLog records the questions asked of the knowledge plane and what
	// came back, which is where a curated evaluation set comes from. It stores
	// what people typed, so it is off unless an operator turns it on.
	RetrievalLog bool
}

func Load() (Config, error) {
	ttl, err := durationEnv("SESSION_TTL", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	timeout, err := durationEnv("LLM_REQUEST_TIMEOUT", 90*time.Second)
	if err != nil {
		return Config{}, err
	}

	ragTimeout, err := durationEnv("RAG_REQUEST_TIMEOUT", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Address:             env("APP_ADDRESS", ":8080"),
		DatabaseURL:         env("DATABASE_URL", "postgres://cosmo:cosmo@localhost:5432/cosmo?sslmode=disable"),
		FrontendURL:         strings.TrimRight(env("FRONTEND_URL", "http://localhost:3000"), "/"),
		SessionSecret:       os.Getenv("SESSION_SECRET"),
		SessionTTL:          ttl,
		CookieSecure:        boolEnv("COOKIE_SECURE", false),
		AdminEmail:          strings.ToLower(strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))),
		PlatformAdminEmails: emailSet(os.Getenv("ADMIN_EMAILS"), os.Getenv("ADMIN_EMAIL")),
		AdminPassword:       os.Getenv("ADMIN_PASSWORD"),
		// Hosts a tool may reach even though they resolve to a private
		// address. Empty means the default: the public internet only. An
		// on-premises deployment names its internal APIs here.
		ToolEgressAllowedHosts: splitAndTrim(os.Getenv("TOOL_EGRESS_ALLOWED_HOSTS")),
		AdminName:              env("ADMIN_NAME", "Cosmo Administrator"),
		EntraTenantID:          strings.TrimSpace(os.Getenv("AZURE_AD_TENANT_ID")),
		EntraClientID:          strings.TrimSpace(os.Getenv("AZURE_AD_CLIENT_ID")),
		EntraClientSecret:      os.Getenv("AZURE_AD_CLIENT_SECRET"),
		EntraRedirectURL:       env("AZURE_AD_REDIRECT_URL", "http://localhost:8080/api/auth/entra/callback"),
		LLMBaseURL:             strings.TrimRight(strings.TrimSpace(os.Getenv("LLM_BASE_URL")), "/"),
		LLMAPIKey:              os.Getenv("LLM_API_KEY"),
		LLMModel:               env("LLM_MODEL", "company-general"),
		LLMSystemPrompt:        env("LLM_SYSTEM_PROMPT", defaultSystemPrompt),
		LLMRequestTimeout:      timeout,
		RAGServiceURL:          strings.TrimRight(strings.TrimSpace(os.Getenv("RAG_SERVICE_URL")), "/"),
		RAGTimeout:             ragTimeout,
		ReindexWorkers:         intEnv("KNOWLEDGE_REINDEX_WORKERS", 4),
		RetrievalLog:           boolEnv("KNOWLEDGE_RETRIEVAL_LOG", false),
	}

	if len(cfg.SessionSecret) < 32 {
		return Config{}, fmt.Errorf("SESSION_SECRET must contain at least 32 characters")
	}
	if cfg.AdminEmail != "" && len(cfg.AdminPassword) < 10 {
		return Config{}, fmt.Errorf("ADMIN_PASSWORD must contain at least 10 characters when ADMIN_EMAIL is configured")
	}
	return cfg, nil
}

func (c Config) EntraEnabled() bool {
	return c.EntraTenantID != "" && c.EntraClientID != "" && c.EntraClientSecret != ""
}

// IsPlatformAdmin checks the email allow-list that is kept outside the
// database. ADMIN_EMAILS accepts a comma-separated list and ADMIN_EMAIL stays
// supported as the initial local-admin bootstrap account.
func (c Config) IsPlatformAdmin(email string) bool {
	return c.PlatformAdminEmails[strings.ToLower(strings.TrimSpace(email))]
}

// KnowledgeEnabled reports whether the knowledge plane is reachable. When it
// is off the product still works; chat simply answers without retrieval.
func (c Config) KnowledgeEnabled() bool {
	return c.RAGServiceURL != ""
}

func (c Config) LLMEnabled() bool {
	return c.LLMBaseURL != "" && c.LLMModel != ""
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func emailSet(values ...string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		for _, email := range strings.Split(value, ",") {
			email = strings.ToLower(strings.TrimSpace(email))
			if email != "" {
				result[email] = true
			}
		}
	}
	return result
}

func boolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func intEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return parsed, nil
}

// splitAndTrim reads a comma-separated environment value into a list, dropping
// blanks so a trailing comma is not read as an empty entry.
func splitAndTrim(raw string) []string {
	list := []string{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			list = append(list, item)
		}
	}
	return list
}

// defaultSystemPrompt is how a plain chat answers when nobody has said
// otherwise. An agent replaces it outright, so this is the workspace's own
// voice rather than a rule imposed on every agent.
//
// The second half is about shape. An answer arrives as a wall of text unless
// it is asked not to, and a reader scanning for one figure should not have to
// read a paragraph to find it. The heading format is spelled out with examples
// because describing it did not work: asked for "a heading with an emoji" the
// model wrote bold numbered items and no emoji at all, and only followed once
// it was shown the exact line to write. A short answer is still a short
// answer - the shape is for answers that have sections, not for every reply.
const defaultSystemPrompt = `Bạn là trợ lý AI nội bộ của doanh nghiệp. Trả lời rõ ràng, chính xác, ngắn gọn và không tự tạo dữ kiện khi thiếu thông tin.

Cách trình bày:
- Câu trả lời ngắn: trả lời thẳng, không tiêu đề, không emoji.
- Câu trả lời dài: chia mục. Mỗi tiêu đề mục viết dạng "### <emoji> <Tiêu đề>", ví dụ "### 🎯 Mục tiêu" hoặc "### ⚠️ Rủi ro". Chọn emoji hợp nội dung từng mục.
- Gạch đầu dòng cho danh sách, bảng cho dữ liệu nhiều cột, khối code cho câu lệnh và mã nguồn.
- In đậm con số và kết luận quan trọng.
- Không rắc emoji giữa câu, không dùng emoji thay cho chữ.`
