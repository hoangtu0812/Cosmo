// Package tools owns what a tool is and what may be done with one. A tool is
// an HTTP integration: one base URL, one credential, and the actions available
// underneath it. The rules and the data access live here; deciding who is
// asking stays with the transport layer, which passes the caller and workspace
// it has already authorised.
package tools

import (
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// Private keeps a tool to its author. Shared offers it to everyone in the
	// workspace it was made in. Same two words agents use, deliberately.
	Private = "private"
	Shared  = "workspace"

	// Authentication styles a tool can carry. Anything the caller sends that
	// is not one of these is refused rather than silently downgraded to none,
	// because silently dropping auth would send the credential nowhere and
	// look like the endpoint simply rejected us.
	AuthNone   = "none"
	AuthBearer = "bearer"
	AuthHeader = "header"

	MaxNameRunes        = 120
	MaxDescriptionRunes = 512
	MaxActions          = 30
	MaxParameters       = 20

	// A tool call sits inside a turn the reader is waiting on, so it gets a
	// short leash. Anything slower belongs in a scheduled run.
	CallTimeout = 20 * time.Second

	// Enough to answer with, small enough that one endpoint cannot exhaust
	// memory or blow the model's context on its own.
	MaxResponseBytes = 64 * 1024
)

// The errors carry the message the reader sees, so the transport layer maps
// them to a status without restating them and the wording stays in one place.
var (
	ErrNotFound        = errors.New("Không tìm thấy tool.")
	ErrNameLength      = errors.New("Tên tool phải từ 1 đến 120 ký tự.")
	ErrDescription     = errors.New("Mô tả tối đa 512 ký tự.")
	ErrBaseURL         = errors.New("API base URL phải là http hoặc https.")
	ErrPrivateAddress  = errors.New("Tool chỉ được gọi ra Internet, không gọi vào địa chỉ nội bộ.")
	ErrAuthType        = errors.New("Kiểu xác thực không hợp lệ.")
	ErrAuthHeaderName  = errors.New("Cần tên header khi xác thực bằng header.")
	ErrActionName      = errors.New("Tên action phải từ 1 đến 120 ký tự và chỉ gồm chữ, số, gạch dưới.")
	ErrActionMethod    = errors.New("Phương thức HTTP không hợp lệ.")
	ErrActionPath      = errors.New("Đường dẫn action phải bắt đầu bằng /.")
	ErrTooManyActions  = errors.New("Mỗi tool tối đa 30 action.")
	ErrTooManyParams   = errors.New("Mỗi action tối đa 20 tham số.")
	ErrDuplicateAction = errors.New("Đã có action trùng tên trong tool này.")
	ErrSecretsOff      = errors.New("Chưa cấu hình khoá mã hoá nên không lưu được thông tin xác thực.")
	ErrCallFailed      = errors.New("Không gọi được endpoint của tool.")
)

// Tool is one HTTP integration the workspace can call.
type Tool struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Tags        []string `json:"tags"`
	OwnerUserID string   `json:"owner_user_id"`
	OwnerName   string   `json:"owner_name"`
	WorkspaceID string   `json:"workspace_id"`
	Visibility  string   `json:"visibility"`
	BaseURL     string   `json:"base_url"`
	// "http" for an API described by hand, "mcp" for a server that
	// describes itself. See internal/tools/mcp.go.
	Kind           string `json:"kind"`
	AuthType       string `json:"auth_type"`
	AuthHeaderName string `json:"auth_header_name"`
	// The secret itself never leaves the server. The hint is the last few
	// characters, which is enough for a reader to tell which key is set.
	AuthHint    string    `json:"auth_hint"`
	HasSecret   bool      `json:"has_secret"`
	ActionCount int       `json:"action_count"`
	IsEditable  bool      `json:"is_editable"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Parameter is one input an action takes. It is described rather than typed in
// Go because its only job is to become JSON Schema for the model.
type Parameter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// "string", "number", "boolean". Anything else is treated as a string,
	// since a wrong type is better than refusing to describe the action.
	Type string `json:"type"`
	// Where the value goes: "query", "path", or "body".
	In         string `json:"in"`
	IsRequired bool   `json:"is_required"`
}

// Action is a single endpoint under a tool - the unit a model calls.
type Action struct {
	ID          string      `json:"id"`
	ToolID      string      `json:"tool_id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Method      string      `json:"method"`
	Path        string      `json:"path"`
	Parameters  []Parameter `json:"parameters"`
	Position    int         `json:"position"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// Changes carries an edit. A nil field means "leave this alone", which lets
// one endpoint serve both a rename and a change of credentials.
type Changes struct {
	Name           *string   `json:"name"`
	Description    *string   `json:"description"`
	Icon           *string   `json:"icon"`
	Tags           *[]string `json:"tags"`
	Visibility     *string   `json:"visibility"`
	BaseURL        *string   `json:"base_url"`
	AuthType       *string   `json:"auth_type"`
	AuthHeaderName *string   `json:"auth_header_name"`
	// An empty string clears the credential; a nil pointer leaves it as it is.
	AuthSecret *string `json:"auth_secret"`
}

// CallResult is what one invocation produced, for the test panel and for the
// step record a run keeps.
type CallResult struct {
	Status      int    `json:"status"`
	DurationMS  int64  `json:"duration_ms"`
	Body        string `json:"body"`
	IsTruncated bool   `json:"is_truncated"`
}

// ValidateName trims and bounds a tool or action title.
func ValidateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > MaxNameRunes {
		return "", ErrNameLength
	}
	return name, nil
}

// ValidateDescription bounds the purpose text by rune, not byte, so a
// Vietnamese description is not cut short by its encoding.
func ValidateDescription(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if utf8.RuneCountInString(text) > MaxDescriptionRunes {
		return "", ErrDescription
	}
	return text, nil
}

// ValidateBaseURL insists on an absolute http(s) URL and strips any trailing
// slash, so joining a path later cannot produce a double slash.
func ValidateBaseURL(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", ErrBaseURL
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", ErrBaseURL
	}
	return strings.TrimRight(text, "/"), nil
}

// NormalizeVisibility narrows anything unrecognised to private. Widening on a
// typo would expose a credentialled integration to the whole workspace.
func NormalizeVisibility(raw string) string {
	if strings.TrimSpace(raw) == Shared {
		return Shared
	}
	return Private
}

// ValidateAuth checks the pairing rather than the fields alone: header auth
// without a header name would send the credential nowhere.
func ValidateAuth(authType, headerName string) (string, string, error) {
	kind := strings.TrimSpace(authType)
	if kind == "" {
		kind = AuthNone
	}
	if kind != AuthNone && kind != AuthBearer && kind != AuthHeader {
		return "", "", ErrAuthType
	}
	name := strings.TrimSpace(headerName)
	if kind == AuthHeader && name == "" {
		return "", "", ErrAuthHeaderName
	}
	if kind != AuthHeader {
		name = ""
	}
	return kind, name, nil
}

// ValidateMethod accepts the verbs the table allows and nothing else.
func ValidateMethod(raw string) (string, error) {
	method := strings.ToUpper(strings.TrimSpace(raw))
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
		return method, nil
	case "":
		return "GET", nil
	default:
		return "", ErrActionMethod
	}
}

// ValidatePath keeps the path relative to the tool's base URL. A caller cannot
// supply an absolute URL here and reach a different host than the one the
// workspace approved.
func ValidatePath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "/", nil
	}
	if !strings.HasPrefix(path, "/") {
		return "", ErrActionPath
	}
	// A protocol-relative path resolves to a different host the moment anything
	// parses it as a URL rather than concatenating it, so it is refused here
	// rather than relied upon to stay harmless.
	if strings.HasPrefix(path, "//") || strings.Contains(path, "://") {
		return "", ErrActionPath
	}
	return path, nil
}

// ValidateActionName bounds the name and keeps it to what a model can call:
// tool names travel in JSON as identifiers, and a space or a dot in one is a
// reliable way to make a model call something that does not exist.
func ValidateActionName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > MaxNameRunes {
		return "", ErrActionName
	}
	for _, r := range name {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if !isLetter && !isDigit && r != '_' {
			return "", ErrActionName
		}
	}
	return name, nil
}

// CleanParameters drops blanks, bounds the list, and settles the two fields
// that decide where a value ends up. The result is never nil, so it encodes as
// [] rather than null.
func CleanParameters(raw []Parameter) ([]Parameter, error) {
	cleaned := make([]Parameter, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		name := strings.TrimSpace(item.Name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		kind := strings.TrimSpace(item.Type)
		if kind != "number" && kind != "boolean" {
			kind = "string"
		}
		where := strings.TrimSpace(item.In)
		if where != "path" && where != "body" {
			where = "query"
		}
		description, err := ValidateDescription(item.Description)
		if err != nil {
			return nil, err
		}
		cleaned = append(cleaned, Parameter{
			Name:        name,
			Description: description,
			Type:        kind,
			In:          where,
			IsRequired:  item.IsRequired,
		})
	}
	if len(cleaned) > MaxParameters {
		return nil, ErrTooManyParams
	}
	return cleaned, nil
}

// CleanStringList trims, drops blanks and bounds a tag list. Mirrors the same
// helper in the agents package rather than importing across domains.
func CleanStringList(raw []string, limit int, maxRunes int) []string {
	cleaned := make([]string, 0, len(raw))
	for _, item := range raw {
		text := strings.TrimSpace(item)
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) > maxRunes {
			text = string([]rune(text)[:maxRunes])
		}
		cleaned = append(cleaned, text)
		if len(cleaned) == limit {
			break
		}
	}
	return cleaned
}
