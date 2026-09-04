package httpapi

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func historyTestServer(t *testing.T) *Server {
	t.Helper()
	url := os.Getenv("COSMO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set COSMO_TEST_DATABASE_URL to test PostgreSQL history snapshots")
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	// A temporary table on a dedicated connection never touches application data.
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, err = pool.Exec(context.Background(), `CREATE TEMP TABLE messages (
		id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, role TEXT NOT NULL,
		content TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{db: pool}
}

func TestChatHistoryRetainsCurrentQuestion(t *testing.T) {
	for _, count := range []int{0, 40, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s := historyTestServer(t)
			ctx := context.Background()
			base := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
			for i := 0; i < count; i++ {
				_, err := s.db.Exec(ctx, `INSERT INTO messages VALUES($1, 'conversation', 'user', $2, $3)`,
					fmt.Sprintf("old-%03d", i), fmt.Sprintf("prior %d", i), base.Add(time.Duration(i)*time.Second))
				if err != nil {
					t.Fatal(err)
				}
			}
			// A newer message in another conversation must not enter the window.
			_, err := s.db.Exec(ctx, `INSERT INTO messages VALUES('other', 'other-conversation', 'user', 'private', $1)`, base.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			question := Message{ID: "current", ConversationID: "conversation", Content: "latest question", CreatedAt: base.Add(time.Hour)}
			history, err := s.recordChatQuestion(ctx, question)
			if err != nil {
				t.Fatal(err)
			}
			want := min(count+1, chatHistoryMessages)
			if len(history) != want || history[want-1].Content != question.Content {
				t.Fatalf("current question missing from history: %#v", history)
			}
			for i := 0; i < want-1; i++ {
				if expected := fmt.Sprintf("prior %d", count-(want-1)+i); history[i].Content != expected {
					t.Fatalf("history[%d] = %q, want %q", i, history[i].Content, expected)
				}
			}
			var saved int
			if err := s.db.QueryRow(ctx, `SELECT count(*) FROM messages WHERE id = 'current'`).Scan(&saved); err != nil || saved != 1 {
				t.Fatalf("question not persisted exactly once: %d, %v", saved, err)
			}
			_, err = s.recordChatQuestion(ctx, Message{ID: "future", ConversationID: "conversation", Content: "future question", CreatedAt: base.Add(2 * time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			if history[len(history)-1].Content != question.Content {
				t.Fatal("later request changed the turn snapshot")
			}
		})
	}
}

func TestChatHistoryTiedTimestamps(t *testing.T) {
	s := historyTestServer(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, id := range []string{"c", "b", "a"} {
		if _, err := s.db.Exec(ctx, `INSERT INTO messages VALUES($1, 'conversation', 'user', $1, $2)`, id, now); err != nil {
			t.Fatal(err)
		}
	}
	// The current ID sorts before older messages; it must still be last.
	history, err := s.recordChatQuestion(ctx, Message{ID: "0", ConversationID: "conversation", Content: "current", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"a", "b", "c", "current"} {
		if len(history) != 4 || history[i].Content != want {
			t.Fatalf("unstable history: %#v", history)
		}
	}
}
