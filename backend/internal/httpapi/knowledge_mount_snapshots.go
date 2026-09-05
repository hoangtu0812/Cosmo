package httpapi

import "context"

// Every entry is explicit: an empty pin means live. Missing entries must never
// silently widen a queued turn to a newly installed source.
func (s *Server) workspaceKnowledgePins(ctx context.Context, workspaceID string) (map[string]string, error) {
	rows, err := s.db.Query(ctx, `SELECT kb.id,COALESCE(m.snapshot_id,'') FROM knowledge_bases kb JOIN knowledge_mounts m ON m.kb_id=kb.id AND m.target_type='workspace' AND m.target_id=$1 WHERE (`+workspaceRetrievableKnowledgeSQL+`)`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pins := map[string]string{}
	for rows.Next() {
		var id, pin string
		if err := rows.Scan(&id, &pin); err != nil {
			return nil, err
		}
		pins[id] = pin
	}
	return pins, rows.Err()
}
