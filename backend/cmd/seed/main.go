// Command seed puts one complete agent into a workspace: prompt, opening
// line, suggested questions, a tool wired up, and a published version. An
// empty screen tells a new reader nothing about what an agent is for, and
// building one by hand to demonstrate the screens is work repeated every time
// the database is reset.
//
// It goes through the repositories rather than SQL so what it writes is what
// the application would have written, validation included.
//
// Running it twice does nothing the second time.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"cosmo/backend/internal/agents"
	"cosmo/backend/internal/secrets"
	"cosmo/backend/internal/tools"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const seedAgentName = "Trợ lý nghiên cứu"

func main() {
	databaseURL := flag.String("database", envOr("DATABASE_URL", "postgres://cosmo:cosmo@localhost:55432/cosmo?sslmode=disable"), "Postgres connection string")
	workspaceID := flag.String("workspace", "", "workspace to seed into (default: the owner's own workspace)")
	ownerEmail := flag.String("owner", "", "email of the user who will own the agent (default: the first user)")
	model := flag.String("model", envOr("SEED_MODEL", "bsr-gpt-4o"), "model the agent answers with")
	flag.Parse()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *databaseURL)
	if err != nil {
		fatal(err)
	}
	defer pool.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	agentRepository := agents.NewRepository(pool, logger)
	// The seeded tool is a built-in, which holds no secret, so an empty box is
	// enough to construct the repository.
	box, err := secrets.New(strings.Repeat("0", 64))
	if err != nil {
		fatal(err)
	}
	toolRepository := tools.NewRepository(pool, logger, box, tools.EgressPolicy{})

	userID, err := resolveUser(ctx, pool, *ownerEmail)
	if err != nil {
		fatal(err)
	}
	if *workspaceID == "" {
		if *workspaceID, err = resolveWorkspace(ctx, pool, userID); err != nil {
			fatal(err)
		}
	}

	var existing string
	err = pool.QueryRow(ctx, `SELECT id FROM agents WHERE owner_workspace_id = $1 AND name = $2`, *workspaceID, seedAgentName).Scan(&existing)
	if err == nil {
		fmt.Printf("agent already seeded: %s\n", existing)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		fatal(err)
	}

	agentID, err := agentRepository.Create(ctx, agents.NewAgent{
		Name:         seedAgentName,
		Introduction: "Tìm hiểu một chủ đề, tóm tắt lại ngắn gọn và nói rõ điều gì còn chưa chắc chắn.",
		Avatar:       "🔭",
		Tags:         []string{"nghiên cứu", "tóm tắt"},
		Visibility:   "workspace",
		OwnerUserID:  userID,
		WorkspaceID:  *workspaceID,
	})
	if err != nil {
		fatal(err)
	}

	current, err := agentRepository.Get(ctx, agentID, userID, *workspaceID)
	if err != nil {
		fatal(err)
	}
	if err := agentRepository.Update(ctx, current, agents.Changes{
		Model:        stringPointer(*model),
		SystemPrompt: stringPointer(systemPrompt),
		OpeningLine:  stringPointer("Bạn đang muốn tìm hiểu về điều gì?"),
		PresetQuestions: &[]string{
			"Tóm tắt giúp tôi những điểm chính của chủ đề này",
			"So sánh hai phương án và nêu rõ đánh đổi",
			"Điều gì trong phần này còn chưa chắc chắn?",
		},
		HasSuggestedQuestions: boolPointer(true),
		IsMemoryEnabled:       boolPointer(true),
	}); err != nil {
		fatal(err)
	}

	// A tool that needs no network and no key, so the seeded agent can be
	// asked to do something on the first message rather than only talk.
	entry, found := tools.CatalogEntryByID("calculator")
	if !found {
		fatal(errors.New("calculator entry missing from the catalogue"))
	}
	tool, _, err := toolRepository.InstallCatalogEntry(ctx, userID, *workspaceID, entry)
	if err != nil {
		fatal(err)
	}
	if err := toolRepository.SetAgentTools(ctx, agentID, userID, *workspaceID, []string{tool.ID}); err != nil {
		fatal(err)
	}

	version, err := agentRepository.Publish(ctx, agentID, userID, "Phiên bản đầu tiên")
	if err != nil {
		fatal(err)
	}
	fmt.Printf("seeded agent %s (%s) in %s, published v%d\n", seedAgentName, agentID, *workspaceID, version.VersionNumber)
}

const systemPrompt = `Bạn là trợ lý nghiên cứu. Nhiệm vụ của bạn là giúp người dùng hiểu một chủ đề, không phải gây ấn tượng.

Cách làm việc:
- Trả lời thẳng vào câu hỏi trước, giải thích sau.
- Khi có tài liệu trong knowledge base, ưu tiên dùng tài liệu đó và ghi rõ nguồn.
- Khi cần tính toán, hãy gọi tool thay vì tự nhẩm.
- Nói rõ điều gì bạn không chắc, thay vì đoán cho trôi câu.
- Viết ngắn. Bỏ những câu chỉ để mở đầu hoặc kết thúc.`

func resolveUser(ctx context.Context, pool *pgxpool.Pool, email string) (string, error) {
	if email != "" {
		var id string
		err := pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, email).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("no user with email %s", email)
		}
		return id, err
	}
	var id string
	err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY created_at LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("no users to own the agent")
	}
	return id, err
}

func resolveWorkspace(ctx context.Context, pool *pgxpool.Pool, userID string) (string, error) {
	var id string
	err := pool.QueryRow(ctx, `
		SELECT w.id FROM workspaces w
		JOIN workspace_memberships m ON m.workspace_id = w.id
		WHERE m.user_id = $1
		ORDER BY w.created_at LIMIT 1`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("that user belongs to no workspace")
	}
	return id, err
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func stringPointer(value string) *string { return &value }
func boolPointer(value bool) *bool       { return &value }

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "seed:", err)
	os.Exit(1)
}
