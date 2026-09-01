package agents

import (
	"context"
	"fmt"
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
func ParseSuggestions(reply string) []string {
	suggestions := []string{}
	for _, line := range strings.Split(reply, "\n") {
		line = strings.TrimSpace(line)
		// Strip the bullet or number a model adds despite being asked not to.
		line = strings.TrimLeft(line, "-*• 0123456789.)")
		line = strings.TrimSpace(line)
		if line == "" || len([]rune(line)) > 200 {
			continue
		}
		suggestions = append(suggestions, line)
		if len(suggestions) == MaxSuggestions {
			break
		}
	}
	return suggestions
}
