# UI dựng trước, chức năng làm sau

Danh sách những chỗ giao diện đã có hình hài nhưng **chưa có chức năng phía
sau**. Tất cả đều bị khoá hoặc nói rõ "chưa có" — không có chỗ nào bấm vào rồi
im lặng không phản hồi.

Mục đích của tài liệu này là để những mục đó không bị quên. Khi làm xong một
mục, gỡ `isDisabled` và xoá dòng tương ứng ở đây.

Đối chiếu với `cosmo_agent_workflow_roadmap.md` để biết mục nào thuộc giai đoạn nào.

## Thanh điều hướng

| Mục | Ở đâu | Cần gì để bật | Giai đoạn |
|---|---|---|---|
| Workflow | Sidebar › Workspace | Graph model, designer, execution engine | 3 |
| Dự án | Sidebar › Workspace | Project, environment, artifact | 4 |
| Lịch chạy | Sidebar › Workspace | Trigger manual/cron/webhook, scheduler lock | 4 |
| Tool | Sidebar › Năng lực | Credential Vault, HTTP/OpenAPI tool, egress allowlist | 2 |
| Skill | Sidebar › Năng lực | Skill registry, dependency lên Tool | 2 |
| Thư viện | Sidebar › Dữ liệu | Registry, release, install/update | 6 |
| Quan sát | Sidebar › Vận hành | Telemetry token/cost/latency, trace, dashboard | 5 |
| Thông báo | Sidebar › Vận hành | Alert, incident, dedupe window | 5 |

## Knowledge base

| Mục | Ở đâu | Cần gì để bật | Giai đoạn |
|---|---|---|---|
| Kiểm tra truy xuất | Chi tiết KB | Đưa `rag-service/app/evaluate.py` thành API và màn hình | 5 |

## Workspace

| Mục | Ở đâu | Cần gì để bật | Giai đoạn |
|---|---|---|---|
| Xoá workspace | Settings › Workspace | Endpoint xoá, dọn KB/agent/hội thoại liên quan | chưa xếp |

## Agent

| Mục | Ở đâu | Cần gì để bật | Giai đoạn |
|---|---|---|---|
| Tab Năng lực | Editor agent | Tool và Skill gắn được vào agent | 2 |
| Nhân bản | Menu ⋯ trên card | Clone definition, không dùng chung cấu hình | 1 (Sprint 4) |
| API key | Menu ⋯ trên card | Cấp key gọi agent từ ngoài | chưa xếp |
| Phân quyền | Menu ⋯ trên card | Quyền chi tiết `view/edit/test/publish/share/delete` | 1 (Sprint 4) |
| Quan sát | Menu ⋯ trên card | Trace theo agent, token/cost | 5 |
| Flow agent | Dialog tạo agent | Workflow engine | 3 |

## Đã dựng UI và **có** chức năng thật

Ghi lại để khỏi nhầm là vỏ:

- Draft tự lưu, cờ chưa xuất bản, nút Xuất bản, changelog, lịch sử phiên bản
- Ghim hội thoại vào phiên bản đã xuất bản
- Panel Debug chạy bản nháp, có inspector đọc lại run
- Ghi nhớ, gợi ý câu hỏi tiếp theo, render markdown
- Tìm kiếm và lọc trên danh sách agent
- Chọn avatar bằng emoji hoặc tải ảnh lên

## Nguyên tắc khi thêm vỏ mới

1. Khoá bằng `isDisabled`, đừng ẩn. Ẩn thì người dùng không biết sản phẩm sẽ
   đi tới đâu; khoá thì biết mà không bị lừa.
2. Nếu component không nhận `isDisabled` (ví dụ `Tab`), để nội dung phía sau nói
   thẳng là chưa có, đừng để trống.
3. Thêm một dòng vào bảng trên. Vỏ không có trong danh sách là vỏ sẽ bị quên.
