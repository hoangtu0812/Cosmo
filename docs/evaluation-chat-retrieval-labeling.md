# Mẫu gán nhãn đánh giá nhiều Knowledge Base

Trạng thái: chưa có bộ câu hỏi nghiệp vụ được duyệt. Đây là mẫu chuẩn bị dữ liệu, không phải kết quả benchmark.

Người phụ trách nghiệp vụ đọc tài liệu gốc, chọn khoảng 25 câu đầu tiên: 5 câu một nguồn, 8 câu cần nhiều nguồn, 5 câu có quy định/điều kiện mâu thuẫn giữa nguồn, 4 câu không có đáp án, 3 câu kiểm tra quyền. Điều chỉnh tỷ lệ theo sử dụng thật; giữ vài câu khó làm tập kiểm tra riêng khi điều chỉnh retrieval.

| Trường | Nội dung cần điền |
|---|---|
| id | Mã ổn định, không chứa dữ liệu nhạy cảm |
| query | Câu hỏi độc lập, đủ mã thiết bị, thời điểm, điều kiện |
| category | Một trong năm nhóm ở hướng dẫn chạy |
| relevant_document_ids | ID tài liệu có bằng chứng đúng; `[]` chỉ khi đã xác nhận không có đáp án |
| required_kb_ids | KB bắt buộc phải có bằng chứng |
| forbidden_kb_ids | KB mà người dùng không được phép nhận nội dung |
| kb_ids | Phạm vi truy vấn tùy chọn; bỏ trường để chọn mọi KB được cài, `[]` không chọn KB |
| evidence_groups | Mỗi nhóm là các tài liệu thay thế được cho một ý; hai phía mâu thuẫn là hai nhóm |

Lưu câu hỏi JSONL bên ngoài Git. Dòng dưới chỉ minh họa cấu trúc; thay placeholder bằng dữ liệu đã kiểm chứng trước khi chạy:

```json
{"id":"Q-001","category":"conflict","query":"<câu hỏi thật cần đối chiếu hai quy định>","relevant_document_ids":["<doc-a>","<doc-b>"],"required_kb_ids":["<kb-a>","<kb-b>"],"evidence_groups":[["<doc-a>"],["<doc-b>"]]}
```

Lưu phiếu duyệt riêng cùng JSONL: người gán nhãn/người duyệt, ngày, snapshot ID, trang/đoạn làm căn cứ, đáp án mong đợi và cách xử lý mâu thuẫn (nguồn ưu tiên/thời hiệu hoặc cần hỏi lại). Phiếu này dùng duyệt đáp án/citation cuối; script hiện chỉ chấm retrieval.

Chạy lần đầu trên các snapshot cố định, xem câu thiếu bằng chứng và độ trễ, thống nhất ngưỡng trước khi chấp nhận report làm baseline. Giữ baseline được duyệt riêng; candidate dùng cùng nhãn/corpus. Không lấy tài liệu hệ thống tự trả về làm nhãn đúng khi chưa đọc nguồn gốc.
