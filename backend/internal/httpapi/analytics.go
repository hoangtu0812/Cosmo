package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// The platform dashboard answers four questions an operator actually has:
// is the platform being used, by which workspaces, through which tools, and is
// any of it failing.
//
// Everything here is derived from tables that already exist - runs, run steps,
// messages and the audit log - rather than from counters kept alongside them.
// A counter drifts the first time a write fails halfway; a query over the
// records themselves cannot disagree with the records.
//
// Counting is by run rather than by message because a run is what a workspace
// costs: one question, one gateway conversation, and every tool call it made.

// analyticsWindow is the period being reported on, and the period before it of
// the same length, which is what turns a number into a direction.
type analyticsWindow struct {
	Days int       `json:"days"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// analyticsInventory is what exists, regardless of when it was made.
type analyticsInventory struct {
	Workspaces     int `json:"workspaces"`
	Users          int `json:"users"`
	KnowledgeBases int `json:"knowledge_bases"`
	Documents      int `json:"documents"`
	Agents         int `json:"agents"`
	Tools          int `json:"tools"`
	Workflows      int `json:"workflows"`
	Conversations  int `json:"conversations"`
}

// analyticsActivity is what happened inside one window.
type analyticsActivity struct {
	Runs             int     `json:"runs"`
	FailedRuns       int     `json:"failed_runs"`
	ActiveWorkspaces int     `json:"active_workspaces"`
	ActiveUsers      int     `json:"active_users"`
	Conversations    int     `json:"conversations"`
	Messages         int     `json:"messages"`
	ToolCalls        int     `json:"tool_calls"`
	FailedToolCalls  int     `json:"failed_tool_calls"`
	Documents        int     `json:"documents"`
	AuditEvents      int     `json:"audit_events"`
	SignIns          int     `json:"sign_ins"`
	FailedSignIns    int     `json:"failed_sign_ins"`
	AvgRunSeconds    float64 `json:"avg_run_seconds"`
}

type analyticsDay struct {
	Date          string `json:"date"`
	Runs          int    `json:"runs"`
	FailedRuns    int    `json:"failed_runs"`
	ActiveUsers   int    `json:"active_users"`
	Messages      int    `json:"messages"`
	Conversations int    `json:"conversations"`
	ToolCalls     int    `json:"tool_calls"`
}

type workspaceActivity struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Type         string     `json:"type"`
	Members      int        `json:"members"`
	Runs         int        `json:"runs"`
	ActiveUsers  int        `json:"active_users"`
	Messages     int        `json:"messages"`
	ToolCalls    int        `json:"tool_calls"`
	LastActiveAt *time.Time `json:"last_active_at,omitempty"`
}

type toolUsage struct {
	Name       string  `json:"name"`
	Action     string  `json:"action,omitempty"`
	Calls      int     `json:"calls"`
	Failures   int     `json:"failures"`
	Workspaces int     `json:"workspaces"`
	AvgMS      float64 `json:"avg_ms"`
}

type agentUsage struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Workspace string `json:"workspace_name"`
	Runs      int    `json:"runs"`
}

// countedItem is any "this many of that" breakdown: run outcomes, models,
// document states, audit domains. One shape means one scan helper.
type countedItem struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type hourlyLoad struct {
	Hour  int `json:"hour"`
	Runs  int `json:"runs"`
	Calls int `json:"tool_calls"`
}

type PlatformAnalytics struct {
	Range        analyticsWindow     `json:"range"`
	Inventory    analyticsInventory  `json:"inventory"`
	Window       analyticsActivity   `json:"window"`
	Previous     analyticsActivity   `json:"previous"`
	Trend        []analyticsDay      `json:"trend"`
	Workspaces   []workspaceActivity `json:"workspaces"`
	Tools        []toolUsage         `json:"tools"`
	ToolActions  []toolUsage         `json:"tool_actions"`
	Agents       []agentUsage        `json:"agents"`
	Models       []countedItem       `json:"models"`
	RunStatus    []countedItem       `json:"run_status"`
	Hourly       []hourlyLoad        `json:"hourly"`
	DocumentSet  []countedItem       `json:"document_status"`
	AuditDomains []countedItem       `json:"audit_domains"`
}

// A run whose step ended in either of these did not do what was asked. Timing
// out is a failure with a different cause, not a different outcome.
const failedStatuses = `('failed', 'timed_out')`

// Sign-in used to be recorded once per provider - auth.local.signed_in and
// auth.entra.signed_in - before the provider moved into the metadata where it
// belongs. The old names are matched here rather than rewritten in the table:
// an audit row records what was written at the time, and a counter that has to
// span a rename is the counter's problem, not the record's.
const signedInActions = `('auth.session.signed_in', 'auth.local.signed_in', 'auth.entra.signed_in')`

func (s *Server) platformAnalytics(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	days := 30
	if parsed, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && parsed >= 1 && parsed <= 365 {
		days = parsed
	}
	to := time.Now()
	from := to.AddDate(0, 0, -days)
	previousFrom := from.AddDate(0, 0, -days)

	ctx := r.Context()
	result := PlatformAnalytics{Range: analyticsWindow{Days: days, From: from, To: to}}
	var err error
	if result.Inventory, err = s.analyticsInventory(ctx); err == nil {
		result.Window, err = s.analyticsActivity(ctx, from, to)
	}
	if err == nil {
		result.Previous, err = s.analyticsActivity(ctx, previousFrom, from)
	}
	if err == nil {
		result.Trend, err = s.analyticsTrend(ctx, from, to)
	}
	if err == nil {
		result.Workspaces, err = s.analyticsWorkspaces(ctx, from, to, 12)
	}
	if err == nil {
		result.Tools, err = s.analyticsTools(ctx, from, to, 12, false)
	}
	if err == nil {
		result.ToolActions, err = s.analyticsTools(ctx, from, to, 12, true)
	}
	if err == nil {
		result.Agents, err = s.analyticsAgents(ctx, from, to, 8)
	}
	if err == nil {
		result.Models, err = s.analyticsCounts(ctx, `
			SELECT COALESCE(NULLIF(model, ''), '—'), COUNT(*)::int FROM messages
			WHERE role = 'assistant' AND created_at >= $1 AND created_at < $2
			GROUP BY 1 ORDER BY 2 DESC LIMIT 8`, from, to)
	}
	if err == nil {
		result.RunStatus, err = s.analyticsCounts(ctx, `
			SELECT status, COUNT(*)::int FROM runs
			WHERE created_at >= $1 AND created_at < $2
			GROUP BY 1 ORDER BY 2 DESC`, from, to)
	}
	if err == nil {
		result.Hourly, err = s.analyticsHourly(ctx, from, to)
	}
	if err == nil {
		// The index is a state, not a flow: what matters is how many documents
		// are ready right now, not how many became ready this month.
		result.DocumentSet, err = s.analyticsCounts(ctx, `
			SELECT status, COUNT(*)::int FROM knowledge_documents GROUP BY 1 ORDER BY 2 DESC`)
	}
	if err == nil {
		result.AuditDomains, err = s.analyticsCounts(ctx, `
			SELECT split_part(action, '.', 1), COUNT(*)::int FROM audit_logs
			WHERE created_at >= $1 AND created_at < $2
			GROUP BY 1 ORDER BY 2 DESC LIMIT 12`, from, to)
	}
	if err != nil {
		s.logger.Error("platform analytics", "error", err)
		writeError(w, http.StatusInternalServerError, "Không thể tải số liệu hoạt động.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) analyticsInventory(ctx context.Context) (analyticsInventory, error) {
	var item analyticsInventory
	err := s.db.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM workspaces),
		       (SELECT COUNT(*) FROM users),
		       (SELECT COUNT(*) FROM knowledge_bases),
		       (SELECT COUNT(*) FROM knowledge_documents),
		       (SELECT COUNT(*) FROM agents),
		       (SELECT COUNT(*) FROM tools),
		       (SELECT COUNT(*) FROM workflows),
		       (SELECT COUNT(*) FROM conversations)`).Scan(
		&item.Workspaces, &item.Users, &item.KnowledgeBases, &item.Documents,
		&item.Agents, &item.Tools, &item.Workflows, &item.Conversations)
	return item, err
}

// analyticsActivity counts one window in a single round trip. Twelve separate
// queries for twelve numbers would be twelve times the latency for an answer
// that is read as one block.
func (s *Server) analyticsActivity(ctx context.Context, from, to time.Time) (analyticsActivity, error) {
	var item analyticsActivity
	err := s.db.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM runs WHERE created_at >= $1 AND created_at < $2),
		       (SELECT COUNT(*) FROM runs WHERE created_at >= $1 AND created_at < $2 AND status IN `+failedStatuses+`),
		       (SELECT COUNT(DISTINCT workspace_id) FROM runs WHERE created_at >= $1 AND created_at < $2),
		       (SELECT COUNT(DISTINCT actor_user_id) FROM runs WHERE created_at >= $1 AND created_at < $2 AND actor_user_id IS NOT NULL),
		       (SELECT COUNT(*) FROM conversations WHERE created_at >= $1 AND created_at < $2),
		       (SELECT COUNT(*) FROM messages WHERE created_at >= $1 AND created_at < $2),
		       (SELECT COUNT(*) FROM run_steps WHERE type = 'tool' AND created_at >= $1 AND created_at < $2),
		       (SELECT COUNT(*) FROM run_steps WHERE type = 'tool' AND status IN `+failedStatuses+` AND created_at >= $1 AND created_at < $2),
		       (SELECT COUNT(*) FROM knowledge_documents WHERE created_at >= $1 AND created_at < $2),
		       (SELECT COUNT(*) FROM audit_logs WHERE created_at >= $1 AND created_at < $2),
		       (SELECT COUNT(*) FROM audit_logs WHERE action IN `+signedInActions+` AND created_at >= $1 AND created_at < $2),
		       (SELECT COUNT(*) FROM audit_logs WHERE action = 'auth.session.sign_in_failed' AND created_at >= $1 AND created_at < $2),
		       (SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (finished_at - started_at))), 0) FROM runs
		        WHERE started_at IS NOT NULL AND finished_at IS NOT NULL AND created_at >= $1 AND created_at < $2)`,
		from, to).Scan(
		&item.Runs, &item.FailedRuns, &item.ActiveWorkspaces, &item.ActiveUsers,
		&item.Conversations, &item.Messages, &item.ToolCalls, &item.FailedToolCalls,
		&item.Documents, &item.AuditEvents, &item.SignIns, &item.FailedSignIns, &item.AvgRunSeconds)
	return item, err
}

// analyticsTrend returns one row per day, including the days nothing happened.
// A line drawn only from days with data hides the gaps, which is the shape a
// reader is looking for.
func (s *Server) analyticsTrend(ctx context.Context, from, to time.Time) ([]analyticsDay, error) {
	rows, err := s.db.Query(ctx, `
		WITH days AS (
			SELECT generate_series($1::date, $2::date, interval '1 day')::date AS day
		), run_days AS (
			SELECT created_at::date AS day, COUNT(*) AS runs,
			       COUNT(*) FILTER (WHERE status IN `+failedStatuses+`) AS failed,
			       COUNT(DISTINCT actor_user_id) AS users
			FROM runs WHERE created_at >= $1 AND created_at < $2 GROUP BY 1
		), message_days AS (
			SELECT created_at::date AS day, COUNT(*) AS messages
			FROM messages WHERE created_at >= $1 AND created_at < $2 GROUP BY 1
		), conversation_days AS (
			SELECT created_at::date AS day, COUNT(*) AS conversations
			FROM conversations WHERE created_at >= $1 AND created_at < $2 GROUP BY 1
		), tool_days AS (
			SELECT created_at::date AS day, COUNT(*) AS calls
			FROM run_steps WHERE type = 'tool' AND created_at >= $1 AND created_at < $2 GROUP BY 1
		)
		SELECT d.day, COALESCE(r.runs, 0)::int, COALESCE(r.failed, 0)::int, COALESCE(r.users, 0)::int,
		       COALESCE(m.messages, 0)::int, COALESCE(c.conversations, 0)::int, COALESCE(t.calls, 0)::int
		FROM days d
		LEFT JOIN run_days r ON r.day = d.day
		LEFT JOIN message_days m ON m.day = d.day
		LEFT JOIN conversation_days c ON c.day = d.day
		LEFT JOIN tool_days t ON t.day = d.day
		ORDER BY d.day`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []analyticsDay{}
	for rows.Next() {
		var day time.Time
		var item analyticsDay
		if err := rows.Scan(&day, &item.Runs, &item.FailedRuns, &item.ActiveUsers,
			&item.Messages, &item.Conversations, &item.ToolCalls); err != nil {
			return nil, err
		}
		item.Date = day.Format("2006-01-02")
		items = append(items, item)
	}
	return items, rows.Err()
}

// analyticsWorkspaces ranks workspaces by what was actually run in them.
// Workspaces with nothing in the window still appear, at the bottom, because
// "which of ours has gone quiet" is the same question asked the other way.
func (s *Server) analyticsWorkspaces(ctx context.Context, from, to time.Time, limit int) ([]workspaceActivity, error) {
	rows, err := s.db.Query(ctx, `
		SELECT w.id, w.name, w.type,
		       (SELECT COUNT(*) FROM workspace_memberships m WHERE m.workspace_id = w.id)::int,
		       COALESCE(r.runs, 0)::int, COALESCE(r.users, 0)::int,
		       COALESCE(msg.messages, 0)::int, COALESCE(t.calls, 0)::int, r.last_active
		FROM workspaces w
		LEFT JOIN (
			SELECT workspace_id, COUNT(*) AS runs, COUNT(DISTINCT actor_user_id) AS users,
			       MAX(created_at) AS last_active
			FROM runs WHERE created_at >= $1 AND created_at < $2 GROUP BY 1
		) r ON r.workspace_id = w.id
		LEFT JOIN (
			SELECT run.workspace_id, COUNT(*) AS calls
			FROM run_steps step JOIN runs run ON run.id = step.run_id
			WHERE step.type = 'tool' AND step.created_at >= $1 AND step.created_at < $2
			GROUP BY 1
		) t ON t.workspace_id = w.id
		LEFT JOIN (
			SELECT c.workspace_id, COUNT(*) AS messages
			FROM messages m JOIN conversations c ON c.id = m.conversation_id
			WHERE m.created_at >= $1 AND m.created_at < $2 GROUP BY 1
		) msg ON msg.workspace_id = w.id
		ORDER BY COALESCE(r.runs, 0) DESC, COALESCE(msg.messages, 0) DESC, w.name
		LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []workspaceActivity{}
	for rows.Next() {
		var item workspaceActivity
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &item.Members,
			&item.Runs, &item.ActiveUsers, &item.Messages, &item.ToolCalls, &item.LastActiveAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// analyticsTools counts what the model actually reached for.
//
// A step's name is the call name the model used - the tool's prefix, two
// underscores, the action - which is why it is split here rather than joined to
// the tools table: a tool deleted last week still explains last week's load,
// and a join would drop it.
func (s *Server) analyticsTools(ctx context.Context, from, to time.Time, limit int, byAction bool) ([]toolUsage, error) {
	action, group := `NULL::text`, `GROUP BY 1`
	if byAction {
		action = `CASE WHEN position('__' in step.name) > 0
		          THEN NULLIF(substring(step.name from position('__' in step.name) + 2), '') END`
		group = `GROUP BY 1, 2`
	}
	rows, err := s.db.Query(ctx, `
		SELECT COALESCE(NULLIF(split_part(step.name, '__', 1), ''), step.name) AS tool_name,
		       `+action+` AS tool_action,
		       COUNT(*)::int, COUNT(*) FILTER (WHERE step.status IN `+failedStatuses+`)::int,
		       COUNT(DISTINCT run.workspace_id)::int,
		       COALESCE(AVG(EXTRACT(EPOCH FROM (step.finished_at - step.started_at)) * 1000)
		                FILTER (WHERE step.started_at IS NOT NULL AND step.finished_at IS NOT NULL), 0)
		FROM run_steps step JOIN runs run ON run.id = step.run_id
		WHERE step.type = 'tool' AND step.created_at >= $1 AND step.created_at < $2
		`+group+` ORDER BY 3 DESC, 1
		LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []toolUsage{}
	for rows.Next() {
		var item toolUsage
		var action *string
		if err := rows.Scan(&item.Name, &action, &item.Calls, &item.Failures, &item.Workspaces, &item.AvgMS); err != nil {
			return nil, err
		}
		if action != nil {
			item.Action = *action
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) analyticsAgents(ctx context.Context, from, to time.Time, limit int) ([]agentUsage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.name, COALESCE(w.name, ''), COUNT(*)::int
		FROM runs r
		JOIN agents a ON a.id = r.resource_id AND r.resource_type = 'agent'
		LEFT JOIN workspaces w ON w.id = a.owner_workspace_id
		WHERE r.created_at >= $1 AND r.created_at < $2
		GROUP BY a.id, a.name, w.name
		ORDER BY 4 DESC, a.name LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []agentUsage{}
	for rows.Next() {
		var item agentUsage
		if err := rows.Scan(&item.ID, &item.Name, &item.Workspace, &item.Runs); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// analyticsHourly is the working day as the platform sees it: every hour is
// present, so a quiet night reads as a quiet night rather than as missing data.
func (s *Server) analyticsHourly(ctx context.Context, from, to time.Time) ([]hourlyLoad, error) {
	hours := make([]hourlyLoad, 24)
	for hour := range hours {
		hours[hour] = hourlyLoad{Hour: hour}
	}
	rows, err := s.db.Query(ctx, `
		SELECT EXTRACT(HOUR FROM r.created_at)::int, COUNT(*)::int,
		       COALESCE(SUM((SELECT COUNT(*) FROM run_steps s WHERE s.run_id = r.id AND s.type = 'tool')), 0)::int
		FROM runs r WHERE r.created_at >= $1 AND r.created_at < $2
		GROUP BY 1 ORDER BY 1`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var hour, runs, calls int
		if err := rows.Scan(&hour, &runs, &calls); err != nil {
			return nil, err
		}
		if hour >= 0 && hour < 24 {
			hours[hour] = hourlyLoad{Hour: hour, Runs: runs, Calls: calls}
		}
	}
	return hours, rows.Err()
}

// analyticsCounts runs any two-column "label, count" query. Every breakdown on
// the dashboard has that shape, and giving each one its own scan loop would be
// the same eight lines eight times.
func (s *Server) analyticsCounts(ctx context.Context, query string, args ...any) ([]countedItem, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []countedItem{}
	for rows.Next() {
		var item countedItem
		if err := rows.Scan(&item.Label, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
