# Chính sách và xác nhận thao tác ghi của tool

## Phạm vi đã triển khai (2026-09-06)

- HTTP GET/HEAD/OPTIONS và builtin mặc định cho phép tự gọi. HTTP mutation và mọi MCP action mặc định cần xác nhận; annotation `readOnlyHint`/`idempotentHint` từ server không tự cấp quyền.
- Chủ sở hữu tool có thể đặt từng action thành `read`, `approval` hoặc `blocked`. Chỉ đánh dấu read sau khi kiểm tra hợp đồng của hệ thống đích. Chính sách gắn với định nghĩa action, schema, tham số cố định, đích, kiểu xác thực và revision tool; thay đổi cấu hình/rediscovery có thể yêu cầu duyệt lại. Block vẫn được giữ khi định nghĩa đổi.
- Chat/workflow chỉ tự gọi action được phân loại read. Action cần xác nhận hiện được thực hiện trong màn hình thử tool: hiển thị đích, action, method/path và tham số rồi xác nhận. Chưa có luồng tạm dừng và tiếp tục approval ngay trong chat/workflow.
- Trước dispatch, backend lưu ledger với actor/workspace/idempotency key và hash nội dung đã duyệt; khóa giao dịch ngăn admission trùng. Cùng key và cùng nội dung thành công trả lại kết quả đã lưu; key khác nội dung bị từ chối.
- Sau admission, đóng tab không hủy lời gọi đã duyệt; lời gọi có timeout 20 giây. Không follow redirect và tắt tái sử dụng kết nối HTTP cho MCP write để tránh replay ngầm.
- Chỉ HTTP 2xx ngoài 202 và không có lỗi transport/protocol (bao gồm MCP isError) được ghi nhận đã nhận kết quả. Generic HTTP chưa diễn giải mã lỗi nghiệp vụ nằm trong body 200; cần adapter của hệ thống đích để xác nhận nghiệp vụ thành công. Lỗi, timeout, 202 hoặc mất tiến trình giữ trạng thái chưa rõ. Không tự retry. Action có thao tác executing/uncertain của cùng actor/workspace bị chặn gửi intent mới.
- Màn hình tool tải lại ledger, hiển thị nội dung đã duyệt để người thực hiện kiểm tra hệ thống đích, chọn đã thực hiện/chưa thực hiện và ghi bằng chứng. Executing quá một phút được đưa vào đối soát. Đối soát không gửi lệnh ra ngoài; key cũ không được tái thực thi.

## Dữ liệu và giới hạn

Ledger lưu arguments, định nghĩa đã duyệt và kết quả để phục vụ đối soát; chúng có thể chứa dữ liệu nghiệp vụ. API giới hạn theo chủ sở hữu tool, actor và workspace. Không lưu khóa xác thực tool trong bản review. Chưa có retention riêng; xóa tool/user/workspace hiện cascade xóa ledger, vì vậy chưa phù hợp yêu cầu lưu vết pháp lý dài hạn.

Bảo đảm hiện tại là tối đa một dispatch cho mỗi key trong ledger còn tồn tại. Đây không phải bảo đảm exactly-once tại SAP/hệ thống đích. Chưa có business idempotency key xuyên người dùng, adapter tra cứu mã giao dịch SAP, đối soát tự động, hoặc kiểm thử end-to-end Entra/SAP thật. Các phần này vẫn thuộc TOOL-01 còn mở. Không chuyển tool ghi thành read để vượt qua đối soát.

## Kiểm thử

Backend/PostgreSQL đã kiểm tra quyền, hash cấu hình, lời gọi đồng thời, replay cùng key, đổi payload, hủy subscriber, kết quả chưa rõ, chặn redirect, đối soát và tiến trình bị ngắt. TypeScript đã qua kiểm tra. Kết quả rollout trên server test được ghi riêng sau triển khai.


### 2026-09-06 — nghiệm thu rollout TOOL-01b trên server test

- Đã triển khai backend/frontend `ced08aa` (policy/ledger từ `24cb652`, khôi phục key sau mất phản hồi từ `ced08aa`, tải nhiều tệp tuần tự từ `05dae36`); RAG giữ bản durable ingestion `38c3908`. Migration hiện tại 36. Backend/frontend healthy và HTTP 200.
- Trước migration đã dừng backend, xác nhận không có chat/ingestion đang chạy, tạo và kiểm tra PostgreSQL backup 418980 bytes, 305 TOC lines tại `.cache/deployments/20260906-tool-write-policy/database.dump`. Image cũ giữ tag `before-tool-write-policy-20260906` cho backend/frontend. Không tự rollback schema đã có ledger ghi.
- Smoke qua API có xác thực với tool HTTP giả lập: chưa duyệt không dispatch; cùng key chỉ một lần; đổi payload bị chặn; lỗi 503 thành uncertain; key cũ và intent mới bị chặn cho đến đối soát; nội dung review và key khôi phục được từ ledger; blocked policy chặn xác nhận.
- Đã SIGKILL backend sau khi endpoint giả lập nhận lệnh. Sau restart vẫn đúng một dispatch, ledger còn nguyên và đối soát được. Chỉ điều chỉnh thời điểm tạo của bản ghi fixture để kiểm tra cửa sổ đối soát một phút. Fixture không gọi SAP thật và đã được dọn sạch.
- Regression chat FIFO, subscriber ngắt kết nối, SSE Last-Event-ID/replay, MCP discovery/rediscovery/invoke đã qua; MCP count_words được chủ sở hữu fixture phân loại read trước khi gọi. Truy xuất 3 tài liệu thực qua gateway hiện tại đã qua, passage thuộc đúng KB. Dữ liệu cuối: 3 tài liệu, 67 chunks; không còn job chat/ingestion/snapshot/write đang chạy.
- Full backend với PostgreSQL đã qua cho phần policy; kiểm thử tools chạy lại sau bản sửa recovery cũng qua, gồm trường hợp hơn 50 operation mới không che mất operation chưa đối soát. TypeScript và Docker production build đã qua. Chưa nghiệm thu thao tác UI bằng trình duyệt hoặc business write SAP thật.
