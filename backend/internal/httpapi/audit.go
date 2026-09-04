package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// The audit log is the record of who changed what. Three rules keep it worth
// keeping:
//
//   - Every action that changes state, grants access or reaches outside Cosmo
//     writes one row. Reads do not: a log that records looking at a list is a
//     log nobody can read.
//   - An action name is `domain.object.verb_past` - `workspace.member.joined`,
//     `tool.action.tested`. The domain is what the console groups by, so it is
//     chosen once per area and never varied.
//   - Metadata carries what changed, never what it changed to when that value
//     is a secret. An API key is recorded as "an API key was set", by its hint
//     at most, and never by its value.

// AuditLog is one recorded action, as the console shows it.
type AuditLog struct {
	ID          int64          `json:"id"`
	ActorID     string         `json:"actor_id,omitempty"`
	ActorName   string         `json:"actor_name"`
	ActorEmail  string         `json:"actor_email"`
	Action      string         `json:"action"`
	TargetType  string         `json:"target_type"`
	TargetID    string         `json:"target_id"`
	TargetLabel string         `json:"target_label,omitempty"`
	WorkspaceID string         `json:"workspace_id,omitempty"`
	Workspace   string         `json:"workspace_name,omitempty"`
	Outcome     string         `json:"outcome"`
	IPAddress   string         `json:"ip_address,omitempty"`
	UserAgent   string         `json:"user_agent,omitempty"`
	RequestID   string         `json:"request_id,omitempty"`
	Metadata    map[string]any `json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
}

// Outcomes. Anything that is not a success is worth being able to filter for on
// its own: a refused action and a failed one are different investigations.
const (
	auditSuccess = "success"
	auditFailure = "failure"
	auditDenied  = "denied"
)

// auditEvent is one action about to be recorded. Only Action is required; the
// rest describe as much of the target as the call site actually knows.
type auditEvent struct {
	Action      string
	TargetType  string
	TargetID    string
	TargetLabel string
	WorkspaceID string
	// Empty means success. A call site that can fail meaningfully says so.
	Outcome  string
	Metadata any
}

// audit records an action by the signed-in caller.
func (s *Server) audit(r *http.Request, event auditEvent) {
	s.auditAs(r, currentUser(r.Context()), event)
}

// auditAs records an action by a named actor, for the handlers that run before
// a session exists: a sign-in that succeeded, and one that did not.
//
// A failed sign-in has no user id at all, only the email that was typed. That
// is the whole reason the actor's email is stored beside the id rather than
// resolved through it.
func (s *Server) auditAs(r *http.Request, actor User, event auditEvent) {
	if event.Outcome == "" {
		event.Outcome = auditSuccess
	}
	payload, err := json.Marshal(event.Metadata)
	if err != nil || event.Metadata == nil {
		payload = []byte(`{}`)
	}
	var actorID *string
	if actor.ID != "" {
		actorID = &actor.ID
	}
	// The write outlives the request that caused it. A browser that navigates
	// away mid-delete cancels the context, and the deletion would then go
	// unrecorded - which is the one case the log exists for.
	ctx := context.WithoutCancel(r.Context())
	if _, err := s.db.Exec(ctx, `
		INSERT INTO audit_logs(actor_user_id, actor_email, actor_name, action, target_type, target_id,
		                       target_label, workspace_id, workspace_name, outcome, ip_address,
		                       user_agent, request_id, metadata)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8,
		       COALESCE((SELECT w.name FROM workspaces w WHERE w.id = $8), ''),
		       $9, $10, $11, $12, $13::jsonb)`,
		actorID, actor.Email, actor.Name, event.Action, event.TargetType, event.TargetID,
		event.TargetLabel, event.WorkspaceID, event.Outcome, clientIP(r),
		truncateRunes(r.UserAgent(), 300), middleware.GetReqID(r.Context()), string(payload)); err != nil {
		s.logger.Warn("write audit log", "action", event.Action, "error", err)
	}
}

// clientIP is the address the request came from. RealIP has already resolved
// the proxy headers, so this only has to drop the port.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

// auditFilter is what the console asked for. Every field is optional, and an
// empty filter is the last page of everything.
type auditFilter struct {
	Action      string
	Domain      string
	ActorID     string
	WorkspaceID string
	TargetType  string
	Outcome     string
	Search      string
	From        *time.Time
	To          *time.Time
	Before      int64
	Limit       int
}

func auditFilterFrom(r *http.Request, defaultLimit, maxLimit int) auditFilter {
	query := r.URL.Query()
	filter := auditFilter{
		Action:      strings.TrimSpace(query.Get("action")),
		Domain:      strings.TrimSpace(query.Get("domain")),
		ActorID:     strings.TrimSpace(query.Get("actor")),
		WorkspaceID: strings.TrimSpace(query.Get("workspace")),
		TargetType:  strings.TrimSpace(query.Get("target_type")),
		Outcome:     strings.TrimSpace(query.Get("outcome")),
		Search:      truncateRunes(query.Get("q"), 100),
		Limit:       defaultLimit,
	}
	if parsed, err := strconv.Atoi(query.Get("limit")); err == nil && parsed > 0 && parsed <= maxLimit {
		filter.Limit = parsed
	}
	// The cursor is the id of the last row already shown. Paging by id rather
	// than by offset keeps a page stable while new events arrive above it.
	if parsed, err := strconv.ParseInt(query.Get("before"), 10, 64); err == nil && parsed > 0 {
		filter.Before = parsed
	}
	if from, err := time.Parse(time.RFC3339, query.Get("from")); err == nil {
		filter.From = &from
	}
	if to, err := time.Parse(time.RFC3339, query.Get("to")); err == nil {
		filter.To = &to
	}
	return filter
}

// where builds the filter clause and its arguments. Values are always bound,
// never interpolated: an audit reader is trusted to look, not to write SQL.
func (filter auditFilter) where() (string, []any) {
	clauses := []string{"TRUE"}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf(clause, len(args)))
	}
	if filter.Action != "" {
		add("a.action = $%d", filter.Action)
	}
	if filter.Domain != "" {
		add("a.action LIKE $%d", filter.Domain+".%")
	}
	if filter.ActorID != "" {
		add("a.actor_user_id = $%d", filter.ActorID)
	}
	if filter.WorkspaceID != "" {
		add("a.workspace_id = $%d", filter.WorkspaceID)
	}
	if filter.TargetType != "" {
		add("a.target_type = $%d", filter.TargetType)
	}
	if filter.Outcome != "" {
		add("a.outcome = $%d", filter.Outcome)
	}
	if filter.From != nil {
		add("a.created_at >= $%d", *filter.From)
	}
	if filter.To != nil {
		add("a.created_at <= $%d", *filter.To)
	}
	if filter.Before > 0 {
		add("a.id < $%d", filter.Before)
	}
	if filter.Search != "" {
		// One box over the fields a reader actually types into: the action, who
		// did it, which workspace, and what was touched.
		add(`(a.action ILIKE '%%' || $%d || '%%'
			OR a.actor_email ILIKE '%%' || $%[1]d || '%%'
			OR a.actor_name ILIKE '%%' || $%[1]d || '%%'
			OR a.workspace_name ILIKE '%%' || $%[1]d || '%%'
			OR a.target_label ILIKE '%%' || $%[1]d || '%%'
			OR a.target_id ILIKE '%%' || $%[1]d || '%%')`, filter.Search)
	}
	return strings.Join(clauses, " AND "), args
}

const auditColumns = `
	a.id, COALESCE(a.actor_user_id, ''), COALESCE(NULLIF(a.actor_name, ''), COALESCE(u.name, '')),
	COALESCE(NULLIF(a.actor_email, ''), COALESCE(u.email, '')),
	a.action, a.target_type, a.target_id, a.target_label,
	a.workspace_id, a.workspace_name, a.outcome, a.ip_address, a.user_agent, a.request_id,
	a.metadata, a.created_at`

func (s *Server) auditRows(ctx context.Context, filter auditFilter) ([]AuditLog, error) {
	where, args := filter.where()
	args = append(args, filter.Limit)
	rows, err := s.db.Query(ctx, `
		SELECT `+auditColumns+`
		FROM audit_logs a LEFT JOIN users u ON u.id = a.actor_user_id
		WHERE `+where+`
		ORDER BY a.id DESC
		LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AuditLog{}
	for rows.Next() {
		var item AuditLog
		var metadata []byte
		if err := rows.Scan(&item.ID, &item.ActorID, &item.ActorName, &item.ActorEmail,
			&item.Action, &item.TargetType, &item.TargetID, &item.TargetLabel,
			&item.WorkspaceID, &item.Workspace, &item.Outcome, &item.IPAddress,
			&item.UserAgent, &item.RequestID, &metadata, &item.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(metadata, &item.Metadata)
		if item.Metadata == nil {
			item.Metadata = map[string]any{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	filter := auditFilterFrom(r, 50, 200)
	// One more than asked for, so the answer can say whether a next page exists
	// without counting the whole table for it.
	filter.Limit++
	items, err := s.auditRows(r.Context(), filter)
	if err != nil {
		s.logger.Error("list audit logs", "error", err)
		writeError(w, http.StatusInternalServerError, "Không thể tải nhật ký audit.")
		return
	}
	hasMore := len(items) == filter.Limit
	if hasMore {
		items = items[:len(items)-1]
	}
	var cursor int64
	if hasMore && len(items) > 0 {
		cursor = items[len(items)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": items, "has_more": hasMore, "next_cursor": cursor})
}

// auditLogFilters is what the console offers to filter by. Read from the log
// itself rather than from a list kept in the frontend, so an action added in
// the API shows up in the picker the first time it happens.
func (s *Server) auditLogFilters(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	type option struct {
		Value string `json:"value"`
		Label string `json:"label"`
		Count int    `json:"count"`
	}
	read := func(query string) ([]option, error) {
		rows, err := s.db.Query(r.Context(), query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []option{}
		for rows.Next() {
			var item option
			if err := rows.Scan(&item.Value, &item.Label, &item.Count); err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, rows.Err()
	}

	actions, err := read(`
		SELECT action, action, COUNT(*)::int FROM audit_logs
		GROUP BY action ORDER BY COUNT(*) DESC, action`)
	if err == nil {
		var domains, workspaces, actors []option
		domains, err = read(`
			SELECT split_part(action, '.', 1), split_part(action, '.', 1), COUNT(*)::int
			FROM audit_logs GROUP BY 1 ORDER BY 3 DESC, 1`)
		if err == nil {
			workspaces, err = read(`
				SELECT workspace_id, MAX(workspace_name), COUNT(*)::int FROM audit_logs
				WHERE workspace_id <> '' GROUP BY workspace_id ORDER BY 3 DESC LIMIT 100`)
		}
		if err == nil {
			actors, err = read(`
				SELECT COALESCE(actor_user_id, ''), MAX(COALESCE(NULLIF(actor_name, ''), actor_email)), COUNT(*)::int
				FROM audit_logs WHERE actor_user_id IS NOT NULL
				GROUP BY actor_user_id ORDER BY 3 DESC LIMIT 100`)
		}
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"actions": actions, "domains": domains, "workspaces": workspaces, "actors": actors,
			})
			return
		}
	}
	s.logger.Error("read audit filters", "error", err)
	writeError(w, http.StatusInternalServerError, "Không thể tải bộ lọc nhật ký audit.")
}

// exportAuditLogs hands the filtered log over as CSV. A compliance review reads
// it in a spreadsheet, not in a browser, and asking an administrator to page a
// screen at a time is asking them to copy it out by hand.
func (s *Server) exportAuditLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	filter := auditFilterFrom(r, 5000, 50000)
	filter.Before = 0
	items, err := s.auditRows(r.Context(), filter)
	if err != nil {
		s.logger.Error("export audit logs", "error", err)
		writeError(w, http.StatusInternalServerError, "Không thể xuất nhật ký audit.")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="cosmo-audit-%s.csv"`, time.Now().Format("20060102-150405")))
	// Excel reads a UTF-8 CSV as the local codepage unless the file says
	// otherwise, which turns every Vietnamese name into mojibake.
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(w)
	defer writer.Flush()
	_ = writer.Write([]string{"time", "action", "outcome", "actor", "actor_email", "workspace",
		"target_type", "target_id", "target_label", "ip_address", "request_id", "metadata"})
	for _, item := range items {
		metadata, _ := json.Marshal(item.Metadata)
		_ = writer.Write([]string{
			item.CreatedAt.UTC().Format(time.RFC3339), item.Action, item.Outcome,
			item.ActorName, item.ActorEmail, item.Workspace,
			item.TargetType, item.TargetID, item.TargetLabel,
			item.IPAddress, item.RequestID, string(metadata),
		})
	}
}
