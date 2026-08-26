package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address           string
	DatabaseURL       string
	FrontendURL       string
	SessionSecret     string
	SessionTTL        time.Duration
	CookieSecure      bool
	AdminEmail        string
	AdminPassword     string
	AdminName         string
	EntraTenantID     string
	EntraClientID     string
	EntraClientSecret string
	EntraRedirectURL  string
	LLMBaseURL        string
	LLMAPIKey         string
	LLMModel          string
	LLMSystemPrompt   string
	LLMRequestTimeout time.Duration
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

	cfg := Config{
		Address:           env("APP_ADDRESS", ":8080"),
		DatabaseURL:       env("DATABASE_URL", "postgres://cosmo:cosmo@localhost:5432/cosmo?sslmode=disable"),
		FrontendURL:       strings.TrimRight(env("FRONTEND_URL", "http://localhost:3000"), "/"),
		SessionSecret:     os.Getenv("SESSION_SECRET"),
		SessionTTL:        ttl,
		CookieSecure:      boolEnv("COOKIE_SECURE", false),
		AdminEmail:        strings.ToLower(strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))),
		AdminPassword:     os.Getenv("ADMIN_PASSWORD"),
		AdminName:         env("ADMIN_NAME", "Cosmo Administrator"),
		EntraTenantID:     strings.TrimSpace(os.Getenv("AZURE_AD_TENANT_ID")),
		EntraClientID:     strings.TrimSpace(os.Getenv("AZURE_AD_CLIENT_ID")),
		EntraClientSecret: os.Getenv("AZURE_AD_CLIENT_SECRET"),
		EntraRedirectURL:  env("AZURE_AD_REDIRECT_URL", "http://localhost:8080/api/auth/entra/callback"),
		LLMBaseURL:        strings.TrimRight(strings.TrimSpace(os.Getenv("LLM_BASE_URL")), "/"),
		LLMAPIKey:         os.Getenv("LLM_API_KEY"),
		LLMModel:          env("LLM_MODEL", "company-general"),
		LLMSystemPrompt:   env("LLM_SYSTEM_PROMPT", "Bạn là trợ lý AI nội bộ của doanh nghiệp. Trả lời rõ ràng, chính xác, ngắn gọn và không tự tạo dữ kiện khi thiếu thông tin."),
		LLMRequestTimeout: timeout,
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

func (c Config) LLMEnabled() bool {
	return c.LLMBaseURL != "" && c.LLMModel != ""
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
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
