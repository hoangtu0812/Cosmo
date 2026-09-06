package httpapi

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
)

// Aggregate only server observations, including auxiliary calls. Unknown
// provider usage stays unknown; no price is inferred from a model's name.
func (s *Server) usageSummary(w http.ResponseWriter, r *http.Request) {
	user, workspace, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || days < 1 || days > 366 {
		writeError(w, 400, "Khoảng thời gian không hợp lệ.")
		return
	}
	audience := r.URL.Query().Get("audience")
	if audience != "workspace" && audience != "me" {
		writeError(w, 400, "Phạm vi không hợp lệ.")
		return
	}
	actor := user.ID
	if audience == "workspace" {
		var allowed bool
		if err = s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2 AND role IN ('owner','admin'))`, workspace, user.ID).Scan(&allowed); err != nil {
			writeError(w, 500, "Không đọc được quyền.")
			return
		}
		if !allowed {
			writeError(w, 403, "Chỉ quản trị workspace được xem tổng hợp toàn workspace.")
			return
		}
		actor = ""
	}
	var raw []byte
	err = s.db.QueryRow(r.Context(), `WITH executions AS (
 SELECT id,resource_type AS kind,created_at,finished_at,status,actor_user_id AS actor FROM runs WHERE workspace_id=$1 AND created_at>=NOW()-$3*INTERVAL '1 day' AND ($2='' OR actor_user_id=$2)
 UNION ALL SELECT id,'workflow',created_at,finished_at,status,actor_id FROM workflow_executions WHERE workspace_id=$1 AND created_at>=NOW()-$3*INTERVAL '1 day' AND ($2='' OR actor_id=$2)
 ), calls AS (
 SELECT st.output AS data FROM run_steps st JOIN executions e ON e.id=st.run_id WHERE st.type='model_call'
 UNION ALL SELECT c.observation FROM workflow_model_calls c JOIN executions e ON e.id=c.execution_id
 UNION ALL SELECT observation FROM rag_model_calls WHERE workspace_id=$1 AND created_at>=NOW()-$3*INTERVAL '1 day' AND ($2='' OR actor_id=$2)
 ), grouped AS (
 SELECT data->>'model' AS model,data->>'phase' AS phase,count(*) AS calls,
 count(*) FILTER(WHERE data->'usage' IS NULL OR data->'usage'='null'::jsonb) AS unknown_usage_calls,
 count(*) FILTER(WHERE data->>'failed'='true') AS failed_calls,
 SUM((data->'usage'->>'prompt_tokens')::bigint) AS prompt_tokens,
 SUM((data->'usage'->>'completion_tokens')::bigint) AS completion_tokens,
 SUM((data->'usage'->>'total_tokens')::bigint) AS total_tokens,
 SUM((data->>'duration_ms')::bigint) AS call_duration_ms
 FROM calls GROUP BY data->>'model',data->>'phase'
 ), daily AS (
 SELECT to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD') AS day,count(*) AS executions,
 count(*) FILTER(WHERE status IN ('failed','interrupted','cancelled')) AS failed_or_stopped,
 AVG(EXTRACT(EPOCH FROM finished_at-created_at)*1000) FILTER(WHERE finished_at IS NOT NULL) AS avg_elapsed_ms
 FROM executions GROUP BY 1
 ) SELECT jsonb_build_object('executions',(SELECT count(*) FROM executions),
 'model_calls',(SELECT count(*) FROM calls),
 'unknown_usage_calls',(SELECT count(*) FROM calls WHERE data->'usage' IS NULL OR data->'usage'='null'::jsonb),
 'known_total_tokens',(SELECT SUM((data->'usage'->>'total_tokens')::bigint) FROM calls),
 'cost',NULL,'cost_status','unconfigured',
 'avg_elapsed_ms',(SELECT AVG(EXTRACT(EPOCH FROM finished_at-created_at)*1000) FROM executions WHERE finished_at IS NOT NULL),
 'groups',COALESCE((SELECT jsonb_agg(to_jsonb(g) ORDER BY model,phase) FROM grouped g),'[]'::jsonb),
 'daily',COALESCE((SELECT jsonb_agg(to_jsonb(d) ORDER BY day) FROM daily d),'[]'::jsonb))`, workspace, actor, days).Scan(&raw)
	if err != nil {
		s.logger.Error("usage summary", "error", err)
		writeError(w, 500, "Không tải được số liệu sử dụng.")
		return
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil {
		writeError(w, 500, "Không đọc được số liệu.")
		return
	}
	applyConfiguredCost(result, workspace, s.cfg.ModelPricesJSON)
	writeJSON(w, 200, result)
}

// Rates are explicitly scoped by workspace and model alias, in USD/million
// tokens. Partial usage/rates yield a subtotal, never a fabricated total.
func applyConfiguredCost(result map[string]any, workspace, configuration string) {
	type rate struct {
		Input  *float64 `json:"input"`
		Output *float64 `json:"output"`
	}
	var prices map[string]map[string]rate
	if json.Unmarshal([]byte(configuration), &prices) != nil {
		return
	}
	groups, _ := result["groups"].([]any)
	known := 0
	missing := 0
	subtotal := float64(0)
	for _, entry := range groups {
		group := entry.(map[string]any)
		model, _ := group["model"].(string)
		calls, _ := group["calls"].(float64)
		unknown, _ := group["unknown_usage_calls"].(float64)
		price, ok := prices[workspace][model]
		if !ok || price.Input == nil || price.Output == nil || *price.Input < 0 || *price.Output < 0 || math.IsNaN(*price.Input) || math.IsNaN(*price.Output) {
			missing += int(calls)
			continue
		}
		prompt, _ := group["prompt_tokens"].(float64)
		completion, _ := group["completion_tokens"].(float64)
		subtotal += (prompt*(*price.Input) + completion*(*price.Output)) / 1000000
		known += int(calls - unknown)
		missing += int(unknown)
	}
	result["unpriced_calls"] = missing
	if known > 0 {
		result["known_cost"] = subtotal
		result["cost_status"] = "partial"
	}
	if missing == 0 && known > 0 {
		result["cost"] = subtotal
		result["cost_status"] = "estimated"
	}
}
