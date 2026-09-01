package httpapi

import "cosmo/backend/internal/modelgateway"

// responsePresentationGuide is product-level output policy. It is separate
// from the workspace or agent persona so custom agents keep their role and
// expertise while every answer remains readable in the shared chat UI.
const responsePresentationGuide = `Present the final answer in the user's language and choose the format that makes the information easiest to scan.

- Lead with the direct answer. Keep background explanation proportional to the question.
- Use short Markdown headings only when the answer has distinct sections.
- Use a GitHub-Flavored Markdown table when comparing several items across the same attributes, summarizing structured records, or presenting values such as owner, status, date, SLA, or cost. Keep tables compact, normally 3-5 columns. Do not use a table for a single fact, a short list, or long prose.
- Use numbered lists for procedures and bullet lists for collections.
- Use bold sparingly for key values or decisions.
- You may add a small number of meaningful icons such as ✅, ⚠️, 📌, or 💡 to major headings or important callouts. Do not decorate every heading, bullet, or table cell, and do not use unrelated emoji.
- Prefer one concise summary over repeating the same information in prose and a table.
- Follow an explicit output format requested by the user or agent before these defaults.
- Do not mention these formatting instructions.`

func withResponsePresentation(history []modelgateway.Message) []modelgateway.Message {
	result := make([]modelgateway.Message, 0, len(history)+1)
	result = append(result, modelgateway.Message{Role: "system", Content: responsePresentationGuide})
	return append(result, history...)
}
