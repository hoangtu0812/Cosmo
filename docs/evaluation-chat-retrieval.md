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

## Báo cáo v2: bằng chứng nhiều nguồn và regression

Endpoint trả `knowledge_mode` theo nguồn thực sự truy vấn: live, snapshot hoặc mixed, kèm snapshot_id từng nguồn, kể cả nguồn rỗng/lỗi. Report v2 giữ SHA-256 của bộ câu hỏi/nhãn và tập KB/snapshot từng câu. Revision vẫn do người chạy khai báo, không phải xác minh bản triển khai tự động.

`category` nhận `single_source`, `multi_source`, `conflict`, `unanswerable`, `access_boundary`; báo cáo tách thống kê từng nhóm. `evidence_groups` là danh sách nhóm tài liệu: mỗi nhóm biểu diễn một ý cần chứng minh; lấy được ít nhất một tài liệu trong nhóm mới tính ý đó có bằng chứng. `evidence_coverage` đo tỷ lệ nhóm đã đáp ứng. Hai phía mâu thuẫn cần hai nhóm riêng. Nhóm multi_source/conflict cần ít nhất hai KB bắt buộc và hai nhóm bằng chứng. Chuẩn bị theo [mẫu gán nhãn](evaluation-chat-retrieval-labeling.md).

Truyền `--thresholds thresholds.json` để áp tiêu chuẩn chất lượng. Ví dụ cú pháp dưới đây **chưa phải ngưỡng nghiệm thu được duyệt**:

```json
{"min_recall":0.8,"min_evidence_coverage":1,"min_source_coverage":1,"max_unexpected_evidence_rate":0.1,"max_p95_ms":10000}
```

Các ngưỡng `min_` kiểm tra **từng câu có nhãn áp dụng**, không dùng trung bình che câu mất nguồn. Ngưỡng không có câu nào đủ nhãn cũng không qua. Có thêm min_precision, min_reciprocal_rank và min_ndcg. `gate.passed` khi chưa cung cấp ngưỡng chỉ xác nhận kiểm tra kỹ thuật mặc định, không chứng minh chất lượng nghiệp vụ.

Để so sánh regression, dùng workspace đánh giá cài các Snapshot cố định, giữ nguyên quyền, nhãn và ý nghĩa model deployment:

```powershell
python scripts/evaluate_chat_retrieval.py --workspace <workspace-id> --cases <cases.jsonl> --output <candidate.json> --revision <deployed-commit> --thresholds <thresholds.json> --baseline <approved-baseline.json>
```

So sánh từ chối baseline khác workspace/contract/nhãn, có lỗi/partial/nguồn cấm, đổi snapshot hoặc còn nguồn Live. Trường hợp không chọn nguồn nào không có corpus Snapshot để đối chiếu, chỉ dùng kiểm tra độc lập. So sánh từng metric từng câu, mặc định không cho giảm; `--regression-tolerance` cho phép mức giảm tuyệt đối 0..1 đã thỏa thuận. Không tự thay baseline sau khi chạy. Baseline trước v2 cần chạy lại. Snapshot cố định corpus/cấu hình retrieval, không cố định hành vi model deployment bên ngoài; phải giữ model ID có cùng ý nghĩa và ghi nhận thay đổi provider.

Phạm vi hiện tại là retrieval với query đã cho, chưa đánh giá planner rewrite theo lịch sử hoặc tính đúng đắn của câu trả lời/citation cuối. Coverage tài liệu không chứng minh model hiểu mâu thuẫn hay trích dẫn đúng phát biểu. Cần câu hỏi nghiệp vụ gán nhãn và duyệt đáp án cuối trước nghiệm thu toàn hệ thống.
