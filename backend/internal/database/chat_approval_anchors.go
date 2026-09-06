package database

var chatApprovalAnchorStatements = []string{
	`ALTER TABLE tool_approvals ADD COLUMN message_id TEXT NOT NULL DEFAULT '', ADD COLUMN call_id TEXT NOT NULL DEFAULT ''`,
	// Only persisted SSE ordering is evidence for legacy ownership. Names and
	// arguments can repeat across turns, so never infer anchors from those.
	`WITH anchors AS (
 SELECT DISTINCT ON (a.id) a.id,t.assistant_message_id,previous.call_id
 FROM tool_approvals a JOIN chat_turn_events e ON e.conversation_id=a.source_id AND e.frame LIKE E'event: approval\n%'
 JOIN chat_turns t ON t.conversation_id=e.conversation_id AND t.client_message_id=e.client_message_id
 CROSS JOIN LATERAL (SELECT (split_part(p.frame,E'\ndata: ',2)::jsonb)->>'id' AS call_id FROM chat_turn_events p WHERE p.conversation_id=e.conversation_id AND p.client_message_id=e.client_message_id AND p.id<e.id AND p.frame LIKE E'event: tool\n%' ORDER BY p.id DESC LIMIT 1) previous
 WHERE a.source_kind='conversation' AND (split_part(e.frame,E'\ndata: ',2)::jsonb)->>'id'=a.id
 ORDER BY a.id,e.id
 ) UPDATE tool_approvals a SET message_id=b.assistant_message_id,call_id=b.call_id FROM anchors b WHERE a.id=b.id AND b.call_id IS NOT NULL`,
	`CREATE INDEX tool_approval_message ON tool_approvals(message_id,call_id) WHERE message_id<>''`,
}
