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

	// A memory rides along on every turn, so it is capped to keep it from
	// crowding out the conversation it is meant to support.
	MaxMemoryRunes = 2000

	// Three is what the Experience tab promises the reader.
	MaxSuggestions = 3
)

// The errors carry the message the reader sees, so the transport layer maps
// them to a status without restating them and the wording stays in one place.
var (
	ErrNameLength            = errors.New("Tên agent phải từ 1 đến 120 ký tự.")
	ErrIntroLength           = errors.New("Giới thiệu tối đa 512 ký tự.")
	ErrNotFound              = errors.New("Không tìm thấy agent.")
	ErrKnowledgeSave         = errors.New("Không thể lưu knowledge base cho agent.")
	ErrKnowledgeNotInstalled = errors.New("Knowledge base chưa được cài vào workspace này.")
)

// Agent is a saved chat configuration: who it is, which model answers as it,
// what it is told to do, and what a reader sees before the first question.
type Agent struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Introduction          string    `json:"introduction"`
	Avatar                string    `json:"avatar"`
	Tags                  []string  `json:"tags"`
	OwnerUserID           string    `json:"owner_user_id"`
	OwnerName             string    `json:"owner_name"`
	WorkspaceID           string    `json:"workspace_id"`
	Visibility            string    `json:"visibility"`
	Model                 string    `json:"model"`
	SystemPrompt          string    `json:"system_prompt"`
	OpeningLine           string    `json:"opening_line"`
	PresetQuestions       []string  `json:"preset_questions"`
	HasSuggestedQuestions bool      `json:"has_suggested_questions"`
	IsMemoryEnabled       bool      `json:"is_memory_enabled"`
	KnowledgeBaseIDs      []string  `json:"knowledge_base_ids"`
	HasAvatarImage        bool      `json:"has_avatar_image"`
	IsEditable            bool      `json:"is_editable"`
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

// Conversation is an agent's own chat history, kept in the same table the
// general chat uses and told apart by the agent id stamped on it.
type Conversation struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Runtime is the configuration a conversation actually runs on: only the
// fields the chat pipeline reads, without the presentation an editor needs.
type Runtime struct {
	Model                 string
	SystemPrompt          string
	KnowledgeBaseIDs      []string
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
