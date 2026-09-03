package agents

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"cosmo/backend/internal/modelgateway"
)

// Memory is what this agent has learned about this person. A missing row is
// normal and means nothing has been learned yet.
func (repository *Repository) Memory(ctx context.Context, agentID, userID string) string {
	var content string
	if err := repository.db.QueryRow(ctx, `
		SELECT content FROM agent_memories WHERE agent_id = $1 AND user_id = $2`,
		agentID, userID).Scan(&content); err != nil {
		return ""
	}
	return strings.TrimSpace(content)
}

// RememberExchange folds the latest question and answer into what the agent
// knows about this person. It runs after the reply has been sent, on a context
// of its own: the request is finished by then, so inheriting its context would
// cancel the call every time. A failure is logged and dropped - forgetting is
// a far smaller harm than making the reader wait on a turn already answered.
func (repository *Repository) RememberExchange(agentID, userID, question, answer string, models *modelgateway.Client, options modelgateway.Options) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	existing := repository.Memory(ctx, agentID, userID)
	prompt := fmt.Sprintf(memoryInstruction, existing, question, answer)
	updated, err := models.Complete(ctx, []modelgateway.Message{{Role: "user", Content: prompt}}, options)
	if err != nil {
		repository.logger.Error("update agent memory", "agent_id", agentID, "error", err)
		return
	}
	updated = strings.TrimSpace(updated)
	if updated == "" || updated == existing {
		return
	}
	if len([]rune(updated)) > MaxMemoryRunes {
		updated = string([]rune(updated)[:MaxMemoryRunes])
	}
	if _, err := repository.db.Exec(ctx, `
		INSERT INTO agent_memories(agent_id, user_id, content, updated_at)
		VALUES($1, $2, $3, NOW())
		ON CONFLICT (agent_id, user_id) DO UPDATE SET content = EXCLUDED.content, updated_at = NOW()`,
		agentID, userID, updated); err != nil {
		repository.logger.Error("save agent memory", "agent_id", agentID, "error", err)
	}
}

// SuggestFollowUps proposes what the reader might ask next. It runs inside the
// request, because the suggestions are part of the reply the reader is waiting
// on, but under a short timeout: a slow suggestion pass must not hold up a
// turn that has already been answered, so it gives up and returns nothing.
func (repository *Repository) SuggestFollowUps(ctx context.Context, question, answer string, models *modelgateway.Client, options modelgateway.Options) []string {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	reply, err := models.Complete(ctx, []modelgateway.Message{
		{Role: "user", Content: fmt.Sprintf(suggestionInstruction, question, answer)},
	}, options)
	if err != nil {
		repository.logger.Error("suggest follow-up questions", "error", err)
		return nil
	}
	return ParseSuggestions(reply)
}

// ParseSuggestions is separate so it can be tested without a model: the model
// is asked for bare questions one per line but is not trusted to comply.
// listMarker is the bullet or number a model adds despite being asked not to.
//
// A pattern rather than a set of characters to trim: trimming every leading
// digit turned "2+2 bằng mấy?" into "+2 bằng mấy?" and did the same to "3+2",
// so two different questions arrived as one broken one, twice. A marker is a
// bullet, or one or two digits followed by a dot or a bracket, and then a
// space - a question that simply opens with a number is not a marker at all.
var listMarker = regexp.MustCompile(`^(?:[-*\x{2022}]|\d{1,2}[.)])\s+`)

func ParseSuggestions(reply string) []string {
	suggestions := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(reply, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(listMarker.ReplaceAllString(line, ""))
		// Duplicates are dropped: three chips, two of them the same question,
		// is two chips and a mistake.
		if line == "" || len([]rune(line)) > 200 || seen[line] {
			continue
		}
		seen[line] = true
		suggestions = append(suggestions, line)
		if len(suggestions) == MaxSuggestions {
			break
		}
	}
	return suggestions
}

// The reference names a conversation after what it was about - "HTTP/3 and
// QUIC relationship" - rather than storing the opening line verbatim. A list
// of truncated first questions is hard to scan, because the distinguishing
// part of a question is rarely in its first hundred characters.
const titleInstruction = `Name this conversation.

Rules:
- At most 6 words. No trailing punctuation, no quotes, no prefix like "Title:".
- Name the subject, not the act of asking. "HTTP/3 and QUIC" - not "Question about HTTP/3".
- Answer in the language the question is written in.
- Reply with the title and nothing else.

Question: %s

Answer: %s`

// MaxTitleRunes keeps a model that ignores the word limit from writing a
// paragraph into the sidebar.
const MaxTitleRunes = 60

// SuggestTitle names a conversation from its first exchange. It runs after the
// answer is saved and its failure costs nothing: the conversation keeps the
// opening line it was already given.
func (repository *Repository) SuggestTitle(ctx context.Context, question, answer string, models *modelgateway.Client, options modelgateway.Options) string {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	reply, err := models.Complete(ctx, []modelgateway.Message{
		{Role: "user", Content: fmt.Sprintf(titleInstruction, question, answer)},
	}, options)
	if err != nil {
		repository.logger.Error("suggest conversation title", "error", err)
		return ""
	}
	return CleanTitle(reply)
}

// CleanTitle is separate so it can be tested without a model: the model is
// asked for a bare title and is not trusted to comply.
func CleanTitle(reply string) string {
	line := strings.TrimSpace(reply)
	if index := strings.IndexAny(line, "\n\r"); index >= 0 {
		line = line[:index]
	}
	line = strings.TrimSpace(line)
	// Models reach for a label and for quotes even when told not to.
	for _, prefix := range []string{"Title:", "title:", "Tiêu đề:", "tiêu đề:"} {
		line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
	}
	line = strings.Trim(line, "\"'“”‘’ ")
	line = strings.TrimRight(line, ".。!?")
	if runes := []rune(line); len(runes) > MaxTitleRunes {
		line = strings.TrimSpace(string(runes[:MaxTitleRunes]))
	}
	return line
}
