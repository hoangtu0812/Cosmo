// Package tools owns what a tool is and what may be done with one. A tool is
// an HTTP integration: one base URL, one credential, and the actions available
// underneath it. The rules and the data access live here; deciding who is
// asking stays with the transport layer, which passes the caller and workspace
// it has already authorised.
package tools

import (
	"encoding/json"
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
	// Offered beyond the owning workspace: to named ones, or to all of them.
	// Only an offer - the other workspace still has to install it.
	Selected = "selected"
	Everyone = "everyone"

	// Authentication styles a tool can carry. Anything the caller sends that
	// is not one of these is refused rather than silently downgraded to none,
	// because silently dropping auth would send the credential nowhere and
	// look like the endpoint simply rejected us.
	AuthNone   = "none"
	AuthBearer = "bearer"
	AuthHeader = "header"

	MaxNameRunes        = 120
	MaxDescriptionRunes = 512
	MaxActions          = 100
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
	ErrNotFound       = errors.New("Không tìm thấy tool.")
	ErrNameLength     = errors.New("Tên tool phải từ 1 đến 120 ký tự.")
	ErrDescription    = errors.New("Mô tả tối đa 512 ký tự.")
	ErrBaseURL        = errors.New("API base URL phải là http hoặc https.")
	ErrPrivateAddress = errors.New("Tool chỉ được gọi ra Internet, không gọi vào địa chỉ nội bộ.")
	// Loopback gets its own wording because it is both the commonest mistake
	// and the one where naming the rule helps least: the reader is looking at a
	// server they can open in a browser, so being told it is unreachable is
	// only confusing until they learn which machine is doing the reaching.
	ErrLoopbackAddress = errors.New("localhost là chính container backend, không phải máy của bạn. Dùng host.docker.internal thay cho localhost, và thêm host đó vào TOOL_EGRESS_ALLOWED_HOSTS.")
	// Setting one is not a typo to correct but a misunderstanding of what the
	// tool is, so it says which of the two it is rather than what to type.
	ErrBuiltinHasNoBaseURL = errors.New("Tool tích hợp sẵn chạy ngay trong hệ thống nên không có API base URL.")
	ErrAuthType            = errors.New("Kiểu xác thực không hợp lệ.")
	ErrAuthHeaderName      = errors.New("Cần tên header khi xác thực bằng header.")
	ErrOAuthConfig         = errors.New("Thông tin OAuth phải gồm token_url, client_id và client_secret.")
	ErrOAuthToken          = errors.New("Máy chủ xác thực không cấp được access token.")
	ErrOBOUnavailable      = errors.New("Hệ thống chưa bật đăng nhập Entra nên chưa dùng được kiểu on-behalf-of.")
	ErrOBONoUser           = errors.New("Tool on-behalf-of chỉ gọi được trong hội thoại có người dùng đăng nhập.")
	ErrOBONoAssertion      = errors.New("Chưa có token Entra của bạn. Hãy đăng xuất rồi đăng nhập lại.")
	ErrActionName          = errors.New("Tên action phải từ 1 đến 120 ký tự và chỉ gồm chữ, số, gạch dưới.")
	ErrMCPToolName         = errors.New("Tên MCP tool phải từ 1 đến 128 byte và chỉ gồm chữ, số, gạch dưới, gạch ngang hoặc dấu chấm.")
	ErrMCPContract         = errors.New("MCP tool contract không hợp lệ hoặc không khớp với tên tool.")
	ErrActionMethod        = errors.New("Phương thức HTTP không hợp lệ.")
	ErrActionPath          = errors.New("Đường dẫn action phải bắt đầu bằng /.")
	ErrTooManyActions      = errors.New("Mỗi tool tối đa 100 action.")
	ErrTooManyParams       = errors.New("Mỗi action tối đa 20 tham số.")
	ErrFixedNeedsValue     = errors.New("Tham số cố định phải có giá trị.")
	ErrDuplicateAction     = errors.New("Đã có action trùng tên trong tool này.")
	ErrSecretsOff          = errors.New("Chưa cấu hình khoá mã hoá nên không lưu được thông tin xác thực.")
	ErrCallFailed          = errors.New("Không gọi được endpoint của tool.")
	// A server that answers 401 or 403 is reachable and working; it is the
	// credential that is missing or wrong. Reporting that as a failure to call
	// sends the reader looking at the network.
	ErrToolUnauthorized = errors.New("MCP server từ chối: thiếu hoặc sai thông tin xác thực.")
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
	// The name of the workspace that owns it, for the workspaces it was
	// offered to: an offer arriving from nobody in particular is hard to
	// judge, and the id says nothing to a reader.
	WorkspaceName string `json:"workspace_name"`
	Visibility    string `json:"visibility"`
	BaseURL       string `json:"base_url"`
	// "http" for an API described by hand, "mcp" for a server that
	// describes itself. See internal/tools/mcp.go.
	Kind           string `json:"kind"`
	AuthType       string `json:"auth_type"`
	AuthHeaderName string `json:"auth_header_name"`
	// The secret itself never leaves the server. The hint is the last few
	// characters, which is enough for a reader to tell which key is set.
	AuthHint    string `json:"auth_hint"`
	HasSecret   bool   `json:"has_secret"`
	ActionCount int    `json:"action_count"`
	// How many agents in the reading workspace have this tool attached. Worth
	// knowing before changing or removing it, which is the moment the question
	// gets asked - and counted per workspace, so a tool offered elsewhere does
	// not report how much of its owner's estate runs on it.
	ReferenceCount int `json:"reference_count"`
	// Whether the workspace framing this read has installed the tool, and
	// whether it lets a plain chat reach for it. Two states, not one: a tool
	// can be installed for an agent's use without answering questions itself.
	IsInstalled bool `json:"is_installed"`
	AutoCall    bool `json:"auto_call"`
	// The live version's number, 0 for a tool never published, and whether the
	// draft has moved since. An agent published from here on calls the live
	// version, so the editor has to be able to say which one that is.
	PublishedVersion      int       `json:"published_version"`
	PublishedVersionID    string    `json:"published_version_id"`
	HasUnpublishedChanges bool      `json:"has_unpublished_changes"`
	IsEditable            bool      `json:"is_editable"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// Parameter is one input an action takes. It is described rather than typed in
// Go because its only job is to become JSON Schema for the model.
type Parameter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// A JSON Schema primitive or container type. HTTP actions normally use the
	// scalar types; MCP's compatibility projection may also show arrays and
	// objects while its complete schema lives in Action.MCPTool.
	Type string `json:"type"`
	// Where the value goes: "query", "path", or "body".
	In         string `json:"in"`
	IsRequired bool   `json:"is_required"`
	// Where the value comes from: "" or "model" for one the model fills in,
	// "fixed" for one the tool always sends. A fixed parameter is never shown
	// to the model - it has no business guessing an API version - so it is
	// absent from the schema and cannot be overridden by a call.
	Source string `json:"source,omitempty"`
	// The value sent when Source is "fixed". Meaningless otherwise.
	Value string `json:"value,omitempty"`
}

// IsFixed reports whether the tool supplies this parameter itself.
func (p Parameter) IsFixed() bool { return p.Source == SourceFixed }

const (
	SourceModel = "model"
	SourceFixed = "fixed"
)

// Action is a single endpoint under a tool - the unit a model calls.
type Action struct {
	ID          string      `json:"id"`
	ToolID      string      `json:"tool_id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Method      string      `json:"method"`
	Path        string      `json:"path"`
	Parameters  []Parameter `json:"parameters"`
	// MCPTool is the complete tool definition returned by tools/list. It is
	// stored intact rather than reconstructed from Parameters, so nested JSON
	// Schema, outputSchema, annotations, icons and _meta survive discovery,
	// publishing and invocation. Empty for HTTP and built-in actions.
	MCPTool json.RawMessage `json:"mcp_tool,omitempty"`
	// What comes back. The type is a hint, not a promise the tool can keep -
	// an API may answer with anything - so nothing is validated against it;
	// both exist to be read by the model before it decides to call.
	ResultType        string    `json:"result_type,omitempty"`
	ResultDescription string    `json:"result_description,omitempty"`
	Position          int       `json:"position"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
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

// BaseURLForKind decides what a tool of this kind may have as a destination.
//
// It exists because the answer differs by kind and was being re-derived at
// every call site: Create and Install skipped the check for a built-in, Update
// did not, and so a built-in could be installed but never renamed. One rule,
// three callers.
//
// A built-in runs in this process, so its only valid destination is none. An
// address arriving for one is not a typo to correct but a misunderstanding of
// what the tool is, and it is refused rather than quietly dropped.
func BaseURLForKind(kind, raw string) (string, error) {
	if kind == KindBuiltin {
		if strings.TrimSpace(raw) != "" {
			return "", ErrBuiltinHasNoBaseURL
		}
		return "", nil
	}
	return ValidateBaseURL(raw)
}

// NormalizeVisibility narrows anything unrecognised to private. Widening on a
// typo would expose a credentialled integration to the whole workspace, and
// now to every workspace, so the direction of the fallback matters more than
// it did.
func NormalizeVisibility(raw string) string {
	switch strings.TrimSpace(raw) {
	case Shared, Selected, Everyone:
		return strings.TrimSpace(raw)
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
	if kind != AuthNone && kind != AuthBearer && kind != AuthHeader && kind != AuthOAuth && kind != AuthOBO {
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
// ValidateResultType keeps the hint to shapes JSON actually has. An empty
// string means the author has not said, which is allowed: an action that
// predates this, or one whose answer resists description.
func ValidateResultType(raw string) string {
	switch strings.TrimSpace(raw) {
	case "string", "number", "integer", "boolean", "object", "array", "null":
		return strings.TrimSpace(raw)
	}
	return ""
}

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
		switch kind {
		case "string", "number", "integer", "boolean", "object", "array", "null":
		default:
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
		source := strings.TrimSpace(item.Source)
		if source != SourceFixed {
			source = SourceModel
		}
		value := strings.TrimSpace(item.Value)
		if source == SourceFixed && value == "" {
			// A fixed parameter with nothing to send is a parameter the author
			// forgot to finish, and silently dropping it would send a request
			// missing something they thought they had set.
			return nil, ErrFixedNeedsValue
		}
		if source == SourceModel {
			value = ""
		}
		cleaned = append(cleaned, Parameter{
			Name:        name,
			Description: description,
			Type:        kind,
			In:          where,
			// "Required" asks the model for something; a fixed value is
			// already there, so the two cannot both be true.
			IsRequired: item.IsRequired && source == SourceModel,
			Source:     source,
			Value:      value,
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
