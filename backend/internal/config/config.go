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
	AdminName           string
	EntraTenantID       string
	EntraClientID       string
	EntraClientSecret   string
	EntraRedirectURL    string
	LLMBaseURL          string
	LLMAPIKey           string
	LLMModel            string
	LLMSystemPrompt     string
	LLMRequestTimeout   time.Duration
	RAGServiceURL       string
	RAGTimeout          time.Duration
	// ReindexWorkers is how many documents a re-index rebuilds at once. Each
	// one occupies the knowledge service and the model gateway, so this is the
	// knob for how much of that capacity a rebuild is allowed to take.
	ReindexWorkers int
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
		AdminName:           env("ADMIN_NAME", "Cosmo Administrator"),
		EntraTenantID:       strings.TrimSpace(os.Getenv("AZURE_AD_TENANT_ID")),
		EntraClientID:       strings.TrimSpace(os.Getenv("AZURE_AD_CLIENT_ID")),
		EntraClientSecret:   os.Getenv("AZURE_AD_CLIENT_SECRET"),
		EntraRedirectURL:    env("AZURE_AD_REDIRECT_URL", "http://localhost:8080/api/auth/entra/callback"),
		LLMBaseURL:          strings.TrimRight(strings.TrimSpace(os.Getenv("LLM_BASE_URL")), "/"),
		LLMAPIKey:           os.Getenv("LLM_API_KEY"),
		LLMModel:            env("LLM_MODEL", "company-general"),
		LLMSystemPrompt:     env("LLM_SYSTEM_PROMPT", "Bạn là trợ lý AI nội bộ của doanh nghiệp. Trả lời rõ ràng, chính xác, ngắn gọn và không tự tạo dữ kiện khi thiếu thông tin."),
		LLMRequestTimeout:   timeout,
		RAGServiceURL:       strings.TrimRight(strings.TrimSpace(os.Getenv("RAG_SERVICE_URL")), "/"),
		RAGTimeout:          ragTimeout,
		ReindexWorkers:      intEnv("KNOWLEDGE_REINDEX_WORKERS", 4),
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
