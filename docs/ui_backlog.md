# UI dựng trước, chức năng làm sau

Danh sách những chỗ giao diện đã có hình hài nhưng **chưa có chức năng phía
sau**. Tất cả đều bị khoá hoặc nói rõ "chưa có" — không có chỗ nào bấm vào rồi
im lặng không phản hồi.

Mục đích của tài liệu này là để những mục đó không bị quên. Khi làm xong một
mục, gỡ `isDisabled` và xoá dòng tương ứng ở đây.

Đối chiếu với `cosmo_agent_workflow_roadmap.md` để biết mục nào thuộc giai đoạn nào.

## Màn hình đã dựng vỏ, chưa có gì phía sau

Đã có route và bố cục giống bản tham chiếu; mọi thao tác ghi đều bị khoá.

| Màn hình | Đường dẫn | Cần gì để bật | Giai đoạn |
|---|---|---|---|
| Dự án | `/projects` | Project, environment, artifact | 4 |
| Lịch chạy | `/schedule` | Trigger manual/cron/webhook, scheduler lock | 4 |
| Thư viện | `/library` | Registry, release, install/update | 6 |
| Thông báo | `/notifications` | Alert, incident, dedupe window | 5 |
| Workflow | `/workflow` | Graph model, designer, execution engine | 3 |
| Tool | `/tools` | Credential Vault, HTTP/OpenAPI tool, egress allowlist | 2 |
| Skill | `/skills` | Skill registry, dependency lên Tool | 2 |
| Quan sát | `/observability` | Telemetry token/cost/latency, trace, dashboard | 5 |
| Giới thiệu khu vực | Header các trang | Nội dung hướng dẫn từng khu | chưa xếp |

## Chat

| Mục | Ở đâu | Cần gì để bật | Giai đoạn |
|---|---|---|---|
| Tệp | Header hội thoại | Đính kèm tệp theo hội thoại, lưu trữ và trích dẫn | chưa xếp |

## Knowledge base

| Mục | Ở đâu | Cần gì để bật | Giai đoạn |
|---|---|---|---|
| Kiểm tra truy xuất | Chi tiết KB | Đưa `rag-service/app/evaluate.py` thành API và màn hình | 5 |

## Tài khoản

| Mục | Ở đâu | Cần gì để bật | Giai đoạn |
|---|---|---|---|
| Truy cập API | Menu tài khoản | Cấp và thu hồi API key cho người dùng | chưa xếp |
| Tham gia workspace | Bộ chuyển workspace | Luồng mời và nhận lời mời | chưa xếp |

## Workspace

| Mục | Ở đâu | Cần gì để bật | Giai đoạn |
|---|---|---|---|
| Xoá workspace | Settings › Workspace | Endpoint xoá, dọn KB/agent/hội thoại liên quan | chưa xếp |

## Agent

| Mục | Ở đâu | Cần gì để bật | Giai đoạn |
|---|---|---|---|
| Tab Năng lực | Editor agent | Tool và Skill gắn được vào agent | 2 |
| Nhân bản | Menu ⋯ trên card | Clone definition, không dùng chung cấu hình | 1 (Sprint 4) |
| Sửa thông tin | Menu ⋯ trên card (agent, KB) | Dialog sửa tên, mô tả, ảnh, thẻ ngay từ danh sách | chưa xếp |
| Cách gọi | Menu ⋯ trên card | Endpoint và ví dụ gọi agent từ ngoài | chưa xếp |
| Phân quyền KB | Menu ⋯ trên card KB | Quyền chi tiết trên từng kho | chưa xếp |
| API key | Menu ⋯ trên card | Cấp key gọi agent từ ngoài | chưa xếp |
| Phân quyền | Menu ⋯ trên card | Quyền chi tiết `view/edit/test/publish/share/delete` | 1 (Sprint 4) |
| Quan sát | Menu ⋯ trên card | Trace theo agent, token/cost | 5 |
| Flow agent | Dialog tạo agent | Workflow engine | 3 |
| Biến trong prompt | Editor agent › Prompt | Thay thế biến khi chạy, khai báo biến theo agent | chưa xếp |
| Tạo bằng AI | Nút Agent mới | Model sinh prompt và cấu hình từ mô tả | chưa xếp |
| Cải thiện prompt | Editor agent › panel | Model viết lại prompt và so sánh kết quả | chưa xếp |

## Đã dựng UI và **có** chức năng thật

Ghi lại để khỏi nhầm là vỏ:

- Draft tự lưu, cờ chưa xuất bản, nút Xuất bản, changelog, lịch sử phiên bản
- Ghim hội thoại vào phiên bản đã xuất bản
- Panel Debug chạy bản nháp, có inspector đọc lại run
- Ghi nhớ, gợi ý câu hỏi tiếp theo, render markdown
- Tìm kiếm và lọc trên danh sách agent
- Chọn avatar bằng emoji hoặc tải ảnh lên
- Ngăn hội thoại gần đây mở từ header, đổi tên và xoá ngay trong ngăn

## Nguyên tắc khi thêm vỏ mới

1. Khoá bằng `isDisabled`, đừng ẩn. Ẩn thì người dùng không biết sản phẩm sẽ
   đi tới đâu; khoá thì biết mà không bị lừa.
2. Nếu component không nhận `isDisabled` (ví dụ `Tab`), để nội dung phía sau nói
   thẳng là chưa có, đừng để trống.
3. Thêm một dòng vào bảng trên. Vỏ không có trong danh sách là vỏ sẽ bị quên.
