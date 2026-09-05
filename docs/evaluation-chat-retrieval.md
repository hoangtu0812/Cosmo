# Đánh giá retrieval trên đường chat

Endpoint `POST /api/workspaces/{workspaceID}/knowledge/retrieve` dùng chính hàm retrieval của chat: resolve quyền/mount, fan-out theo cấu hình KB, fusion và trạng thái từng nguồn. Session phải thuộc workspace. `kb_ids` chỉ thu hẹp quyền; `[]` không chọn KB nào, bỏ trường này chọn mọi KB đang được phép trong workspace.

Đầu vào: `{"query":"câu hỏi đã kiểm chứng","kb_ids":["ID thực tế"]}`. Kết quả gồm passages, sources, incomplete, duration_ms và contract `chat-go-v1`. Không chạy model/tool nghiệp vụ; embedding/reranking có thể phát sinh chi phí theo cấu hình KB hiện tại.

Chuẩn bị JSONL, mỗi dòng có `id`, `query`, `relevant_document_ids` do người đọc tài liệu gốc gán nhãn. Có thể thêm `required_kb_ids`, `forbidden_kb_ids`, `kb_ids`. Mảng relevant rỗng có nghĩa người gán nhãn xác nhận không có đáp án. Không tự lấy citation do model chọn làm ground truth.

Chạy từ thư mục dự án, đặt session được phép trong biến môi trường `COSMO_EVAL_SESSION`:

```powershell
python scripts/evaluate_chat_retrieval.py --workspace <workspace-id> --cases <cases.jsonl> --output <report.json> --revision <deployed-commit>
```

Report không chứa session, câu hỏi hay toàn văn passage; vẫn có ID tài liệu/nguồn nên lưu trong nơi có kiểm soát truy cập. Redirect bị từ chối để không chuyển session sang đích khác. HTTP failure và partial được giữ trong tổng số trường hợp; điểm relevance ghi rõ mẫu số có response, không dùng điểm trung bình đó để che lỗi.

Đo ở mức tài liệu: recall, precision, reciprocal rank, nDCG nhị phân, coverage KB bắt buộc, nguồn bị cấm và bằng chứng bất ngờ cho câu không có đáp án; có p50/p95 latency retrieval. Exit code khác 0 khi có failure/partial/nguồn bị cấm. Chưa đặt ngưỡng relevance vì chưa có baseline nghiệp vụ được duyệt.

Phạm vi hiện tại là retrieval với query đã cho, chưa đánh giá planner rewrite theo lịch sử hoặc câu trả lời/citation cuối. Dữ liệu KB đang live; revision là bản code triển khai, không phải snapshot corpus. Không tuyên bố báo cáo có thể tái tạo trên corpus đã thay đổi. Cần bổ sung index generation/snapshot, cấu hình provider và bộ câu hỏi nghiệp vụ trước nghiệm thu chất lượng toàn hệ thống.
