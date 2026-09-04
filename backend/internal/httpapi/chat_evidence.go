package httpapi

const partialKnowledgeNotice = "Lưu ý: một số nguồn Knowledge Base chưa truy cập được. Câu trả lời dưới đây chỉ dựa trên các nguồn đã tìm được, nên có thể chưa đủ để đối chiếu toàn bộ.\n\n"

// A failed or empty required lookup cannot establish internal policy. Return
// an explicit answer without asking a model to fill that evidentiary gap.
func missingKnowledgeAnswer(required bool, passages int, failed bool) string {
	if !required || passages > 0 {
		return ""
	}
	if failed {
		return "Mình chưa truy cập được đầy đủ nguồn Knowledge Base cần thiết để xác minh câu hỏi này. Bạn có thể thử lại sau hoặc cung cấp tài liệu liên quan; hiện mình chưa thể kết luận về thông tin nội bộ đó."
	}
	return "Mình chưa tìm thấy bằng chứng phù hợp trong các Knowledge Base được phép tra cứu để trả lời câu hỏi này. Bạn có thể bổ sung tên tài liệu, mã quy trình hoặc thông tin cụ thể hơn."
}
