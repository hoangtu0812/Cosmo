package httpapi

import (
	"context"
	"cosmo/backend/internal/knowledge"
	"encoding/json"
	"time"
)

func (s *Server) observeRAGModel(ctx context.Context, settings knowledge.ModelSettings, observation knowledge.Observation) {
	switch observation.Phase {
	case "rag:ingest:embedding", "rag:search:embedding", "rag:search:rerank":
	default:
		return
	}
	if settings.EmbeddingScope == "" || len(observation.ID) != 36 || len(observation.Model) > 200 || observation.DurationMS < 0 {
		return
	}
	if u := observation.Usage; u != nil && (u.PromptTokens < 0 || u.CompletionTokens != 0 || u.TotalTokens < u.PromptTokens || u.TotalTokens > 1e12) {
		observation.Usage = nil
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		return
	}
	// Accounting belongs to the workspace paying for the KB gateway, which may
	// differ from a workspace mounting that KB. Record only the requesting actor.
	save, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = s.db.Exec(save, `INSERT INTO rag_model_calls(id,workspace_id,actor_id,observation) VALUES($1,$2,NULLIF($3,''),$4) ON CONFLICT(id) DO NOTHING`, observation.ID, settings.EmbeddingScope, currentUser(ctx).ID, raw)
	if err != nil {
		s.logger.Warn("could not persist RAG accounting", "error", err)
	}
}
