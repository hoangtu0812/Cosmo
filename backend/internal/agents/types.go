// Package agents owns what an agent is and what may be done to one. It holds
// the data access and the rules; deciding who is asking stays with the
// transport layer, which passes the caller and workspace it has authorised.
package agents

import (
	"errors"
	"time"
)

const (
	// Private keeps an agent to its author. Shared offers it to everyone in
	// the workspace it was made in.
	Private = "private"
	Shared  = "workspace"

	MaxKnowledgeBases  = 5
	MaxPresetQuestions = 10
	MaxNameRunes       = 120
	MaxIntroRunes      = 512

	// A changelog is a note about one publish, not a document. Capping it
	// keeps a version row from growing without bound.
	MaxChangelogRunes = 500

	// A memory rides along on every turn, so it is capped to keep it from
	// crowding out the conversation it is meant to support.
	MaxMemoryRunes = 2000

	// Three is what the Experience tab promises the reader.
	MaxSuggestions = 3
)

// The errors carry the message the reader sees, so the transport layer maps
// them to a status without restating them and the wording stays in one place.
var (
	// ErrStaleDraft means another editor saved since this one loaded the
	// draft. Refusing is the point: the alternative is losing their work.
	ErrStaleDraft            = errors.New("Agent đã được người khác sửa. Hãy tải lại trước khi lưu.")
	ErrRevisionRequired      = errors.New("Cần phiên bản draft hiện tại để lưu agent. Hãy tải lại trước khi lưu.")
	ErrDraftForbidden        = errors.New("Chỉ người có quyền sửa agent mới được chạy draft.")
	ErrUnpublished           = errors.New("Agent chưa có phiên bản phát hành. Hãy publish trước khi sử dụng.")
	ErrToolReleaseRequired   = errors.New("Mọi tool gắn vào agent phải có phiên bản phát hành hợp lệ trước khi publish agent.")
	ErrNothingToPublish      = errors.New("Không có thay đổi nào để xuất bản.")
	ErrNameLength            = errors.New("Tên agent phải từ 1 đến 120 ký tự.")
	ErrIntroLength           = errors.New("Giới thiệu tối đa 512 ký tự.")
	ErrNotFound              = errors.New("Không tìm thấy agent.")
	ErrKnowledgeSave         = errors.New("Không thể lưu knowledge base cho agent.")
	ErrKnowledgeNotInstalled = errors.New("Knowledge base chưa được cài vào workspace này.")
)

// Agent is a saved chat configuration: who it is, which model answers as it,
// what it is told to do, and what a reader sees before the first question.
type Agent struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Introduction          string   `json:"introduction"`
	Avatar                string   `json:"avatar"`
	Tags                  []string `json:"tags"`
	OwnerUserID           string   `json:"owner_user_id"`
	OwnerName             string   `json:"owner_name"`
	WorkspaceID           string   `json:"workspace_id"`
	Visibility            string   `json:"visibility"`
	Model                 string   `json:"model"`
	SystemPrompt          string   `json:"system_prompt"`
	OpeningLine           string   `json:"opening_line"`
	PresetQuestions       []string `json:"preset_questions"`
	HasSuggestedQuestions bool     `json:"has_suggested_questions"`
	IsMemoryEnabled       bool     `json:"is_memory_enabled"`
	KnowledgeBaseIDs      []string `json:"knowledge_base_ids"`
	HasAvatarImage        bool     `json:"has_avatar_image"`
	IsEditable            bool     `json:"is_editable"`
	// DraftRevision is what a save must carry back to prove it read the
	// current draft; a stale one is refused rather than silently overwriting.
	DraftRevision int64 `json:"draft_revision"`
	// PublishedVersion is empty while an agent has never been published.
	PublishedVersion      int       `json:"published_version"`
	PublishedVersionID    string    `json:"published_version_id"`
	HasUnpublishedChanges bool      `json:"has_unpublished_changes"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// NewAgent is what creating one requires. The workspace and owner are settled
// by the caller, which is the only party that can authorise them.
type NewAgent struct {
	Name         string
	Introduction string
	Avatar       string
	Tags         []string
	Visibility   string
	OwnerUserID  string
	WorkspaceID  string
}

// Changes is a partial update: the editor saves one tab at a time, so a field
// left nil keeps what is stored rather than blanking it.
type Changes struct {
	Name                  *string
	Introduction          *string
	Avatar                *string
	Tags                  *[]string
	Visibility            *string
	Model                 *string
	SystemPrompt          *string
	OpeningLine           *string
	PresetQuestions       *[]string
	HasSuggestedQuestions *bool
	IsMemoryEnabled       *bool
	KnowledgeBaseIDs      *[]string
}

// Version is an immutable snapshot of a draft at the moment it was published.
type Version struct {
	ID                    string    `json:"id"`
	AgentID               string    `json:"agent_id"`
	VersionNumber         int       `json:"version_number"`
	Model                 string    `json:"model"`
	SystemPrompt          string    `json:"system_prompt"`
	OpeningLine           string    `json:"opening_line"`
	PresetQuestions       []string  `json:"preset_questions"`
	HasSuggestedQuestions bool      `json:"has_suggested_questions"`
	IsMemoryEnabled       bool      `json:"is_memory_enabled"`
	KnowledgeBaseIDs      []string  `json:"knowledge_base_ids"`
	Changelog             string    `json:"changelog"`
	PublishedBy           string    `json:"published_by"`
	CreatedAt             time.Time `json:"created_at"`
}

// Conversation is an agent's own chat history, kept in the same table the
// general chat uses and told apart by the agent id stamped on it.
type Conversation struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Title       string `json:"title"`
	// Empty means the conversation follows the draft rather than a frozen
	// version, and VersionNumber is then 0.
	AgentVersionID string    `json:"agent_version_id"`
	VersionNumber  int       `json:"version_number"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Runtime is the configuration a conversation actually runs on: only the
// fields the chat pipeline reads, without the presentation an editor needs.
type Runtime struct {
	Model            string
	SystemPrompt     string
	KnowledgeBaseIDs []string
	// Empty when the conversation runs the draft rather than a published
	// version; the caller then reads the live attachment instead.
	ToolIDs []string
	// Which version of each of those tools the agent was published against,
	// keyed by tool id. Legacy releases with missing pins fail execution and
	// require review and publication of a new agent version.
	ToolVersions          map[string]string
	IsMemoryEnabled       bool
	HasSuggestedQuestions bool
}

// MemoryHeader introduces a memory where it is injected into a turn.
const MemoryHeader = `Điều đã biết về người dùng này:
`

// memoryInstruction asks for the whole memory back rather than a diff: merging
// is the model's job, and a diff would need a second pass to apply.
const memoryInstruction = `Bạn đang duy trì trí nhớ dài hạn về một người dùng.
Ghi lại những điều bền vững, hữu ích cho các lần trò chuyện sau: vai trò, lĩnh vực phụ trách,
cách họ muốn được trả lời, các ràng buộc họ đã nêu.
Bỏ qua nội dung nhất thời của riêng câu hỏi này.
Trả về TOÀN BỘ trí nhớ sau khi cập nhật, mỗi ý một dòng, tối đa 15 dòng, không thêm lời dẫn.
Nếu không có gì đáng nhớ, trả về đúng phần trí nhớ hiện có.

Trí nhớ hiện có:
%s

Người dùng hỏi:
%s

Agent trả lời:
%s`

// suggestionInstruction asks for bare questions, one per line. The reply is
// split on newlines and anything that does not look like a question is
// dropped, so a model that adds a preamble degrades to fewer suggestions
// rather than putting prose in a button.
const suggestionInstruction = `Dựa trên đoạn hội thoại dưới đây, hãy đề xuất 3 câu hỏi tiếp theo mà người dùng có thể muốn hỏi.
Mỗi câu một dòng, không đánh số, không thêm lời dẫn, không dùng dấu gạch đầu dòng.
Viết bằng ngôn ngữ mà người dùng đang dùng.

Người dùng hỏi:
%s

Agent trả lời:
%s`
