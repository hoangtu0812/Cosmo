package httpapi

import (
	"context"
	"fmt"
	"strings"

	"cosmo/backend/internal/modelgateway"
)

// Deciding what a turn needs before doing it.
//
// Every question used to go through the knowledge base whether it had anything
// to do with the documents or not - "who am I" came back with a citation to a
// remote-access procedure, which is worse than no citation: it says the answer
// came from a document it never read.
//
// So the turn reads the question first and decides. One small call, only where
// there is something to decide: a workspace with no knowledge mounted skips it
// entirely, and a failure searches anyway rather than quietly dropping the
// grounding a correct answer might have needed.

// turnPlan is what the turn decided to do before doing it.
type turnPlan struct {
	NeedsKnowledge bool
	// Why, in the model's own words, for the run record. Never shown as prose
	// to the reader: the status line says what is happening, not why.
	Reason string
}

const planInstruction = `Bạn quyết định một việc duy nhất: câu hỏi này có cần tra cứu tài liệu nội bộ không.

Cần tra cứu khi câu hỏi hỏi về quy trình, quy định, tài liệu, hệ thống hoặc dữ liệu của tổ chức.
Không cần khi câu hỏi nói về chính người dùng hoặc phiên làm việc (tôi là ai, tôi ở workspace nào),
là lời chào, là yêu cầu tính toán hay dịch thuật, là kiến thức phổ thông,
hoặc là hỏi về tệp người dùng vừa đính kèm - tệp đó đã có sẵn, không cần tra cứu thêm.

Chỉ trả lời đúng một từ: CO hoặc KHONG.`

// planTurn asks whether this question wants the knowledge base.
//
// It is deliberately a single word in and out. Anything richer invites the
// model to answer the question here instead of planning it, and this call is
// paid for on every turn.
func (s *Server) planTurn(ctx context.Context, models *modelgateway.Client, options modelgateway.Options, question string, topics []string, attached []string) turnPlan {
	// Nothing mounted, nothing to decide.
	if len(topics) == 0 {
		return turnPlan{NeedsKnowledge: false, Reason: "workspace không có knowledge base nào"}
	}
	if strings.TrimSpace(question) == "" {
		return turnPlan{NeedsKnowledge: false, Reason: "câu hỏi trống"}
	}

	prompt := fmt.Sprintf("%s\n\nTài liệu có sẵn: %s\n\nCâu hỏi: %s",
		planInstruction, strings.Join(topics, ", "), question)
	// Complete drops the client's system prompt, so an agent's instructions do
	// not lean on this decision - it judges the question, not the persona.
	answer, err := models.Complete(ctx, []modelgateway.Message{{Role: "user", Content: prompt}}, planOptions(options))
	if err != nil {
		// A planner that cannot be reached must not be the reason an answer
		// loses its evidence.
		return turnPlan{NeedsKnowledge: true, Reason: "không hỏi được kế hoạch: " + err.Error()}
	}

	decision := strings.ToUpper(strings.TrimSpace(answer))
	switch {
	case strings.HasPrefix(decision, "KHONG"), strings.HasPrefix(decision, "KHÔNG"):
		return turnPlan{NeedsKnowledge: false, Reason: "câu hỏi không cần tài liệu nội bộ"}
	case strings.HasPrefix(decision, "CO"), strings.HasPrefix(decision, "CÓ"):
		return turnPlan{NeedsKnowledge: true, Reason: "câu hỏi cần tài liệu nội bộ"}
	}
	// An answer in neither shape is a planner that did not understand the job;
	// searching is the safer reading of that.
	return turnPlan{NeedsKnowledge: true, Reason: "kế hoạch không rõ: " + decision}
}

// planOptions keeps the turn's model and drops its reasoning budget: the
// planner produces one word, and an effort setting chosen for the answer would
// be spent deciding whether to search. Empty omits the parameter entirely,
// which is also the only setting every model accepts.
func planOptions(options modelgateway.Options) modelgateway.Options {
	return modelgateway.Options{Model: options.Model}
}
