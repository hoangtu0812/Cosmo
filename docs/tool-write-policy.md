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


## TOOL-01c — xác nhận ngay trong chat/workflow (2026-09-06)

Màn hình chat và sidebar workflow nay tải yêu cầu xác nhận trực tiếp theo actor/workspace/conversation hoặc workflow. Người tạo tool xem đích, action, method/path và arguments đã áp dụng giá trị cố định, rồi xác nhận hoặc từ chối. Quyết định ghi một lần vào PostgreSQL; model không được cung cấp cờ để tự cấp quyền. Worker đang chờ mới được gọi `InvokeConfirmed` với key do backend sinh, sau đó tiếp tục lịch sử tool hoặc node tiếp theo bằng kết quả thật.

Yêu cầu chờ tối đa 90 giây và luôn nằm trong deadline phiên chạy. Lease 5 giây giữ yêu cầu gắn với executor còn sống; context hủy, lease hết hoặc từ chối đều không dispatch. Trước dispatch vẫn kiểm tra membership, chủ sở hữu tool và hash định nghĩa qua ledger. Sau mất tiến trình, API đọc kết quả từ ledger ngay cả khi approval row chưa được cập nhật, nên uncertain vẫn dẫn tới màn hình đối soát.

Phạm vi quyền hiện tại vẫn là người tạo tool tự xác nhận thao tác của mình. Chưa triển khai người phê duyệt riêng cho shared tool. Chat đóng tab vẫn có worker tiếp tục và có thể tải lại yêu cầu đang chờ; workflow đóng kết nối trước admission thì dừng phiên. Chờ phê duyệt vẫn chiếm một worker chat; chưa có checkpoint để giải phóng worker và tự khôi phục phần còn lại sau restart. Restart không tự tiếp tục hay phát lại lệnh đã duyệt. Đây là giới hạn còn mở của durable approval/resume, không phải xác nhận hoàn thành toàn bộ TOOL-01.

Kiểm thử PostgreSQL: actor khác, sai definition, quyết định lặp, approve/reject, hết lease, hủy phiên, thay đổi tool, mất membership và khôi phục operation từ ledger; full backend tests và TypeScript đã qua. Rollout API được ghi riêng sau triển khai.


### Nghiệm thu TOOL-01c trên server test

Backend `be36bff`, frontend bổ sung khung thử agent tại `fa70910`, migration 37. Backup trước migration đã kiểm tra: 426988 bytes, 319 TOC lines. Smoke xác thực qua API cho chat approve/reject và workflow approve/reject: đúng 2 lệnh được nhận cho 2 lần duyệt, 0 lệnh khi từ chối; replay chat và lặp quyết định không dispatch thêm. SIGKILL backend trong lúc workflow đang chờ duyệt rồi restart: yêu cầu cũ hết hạn và không thể gửi lệnh. Regression chat FIFO/SSE/replay, MCP discovery/invoke và 3 tài liệu KB thực đều qua. Fixture đã dọn sạch. Chưa kiểm tra tương tác UI bằng trình duyệt.


### TOOL-01d — checkpoint và tiếp tục workflow đã lưu

Migration 38 lưu từng phiên workflow, input/model, hash graph/tool/gateway, node đang chạy và output/branch của bước đã hoàn tất. Trước khi gọi node phải lưu admission; sau khi chạy phải lưu kết quả rồi mới sang node kế tiếp. Giao diện liệt kê phiên gần đây và cho phép tiếp tục phiên bị gián đoạn. Resume dùng input/model cũ, kiểm tra actor/workspace/quyền workflow và hash runtime, cấp lease owner mới để chặn writer cũ. Mỗi actor/workflow chỉ có một phiên đang chạy.

Bước hoàn tất được phục hồi từ checkpoint. Nếu bị ngắt ở tool đang chờ duyệt, backend chỉ tiếp tục khi có bằng chứng chưa dispatch: không có ledger và approval còn pending/expired; approval cũ bị vô hiệu hóa trong giao dịch rồi mới yêu cầu duyệt mới. Nếu ledger đã succeeded nhưng checkpoint chưa lưu, dùng chính response đã lưu để hoàn tất node, không gửi lại. Executing/uncertain, quyết định từ chối, hoặc node đang chạy mà không có bằng chứng kết quả đều bị từ chối resume. Đối soát thủ công đã thực hiện chưa đủ để tái tạo output cho node sau; không tự đoán kết quả.

Đây là tiếp tục thủ công bằng checkpoint sau gián đoạn, chưa phải worker workflow tự chạy nền hay tự thức dậy sau restart. Node LLM/read đang chạy khi bị ngắt vẫn bị chặn bảo thủ; graph/gateway/tool thay đổi yêu cầu một phiên mới có chủ ý. Checkpoint lưu input/output nghiệp vụ với quyền actor/workspace, chưa có retention riêng. Durable chat checkpoint và giải phóng worker khi chờ duyệt vẫn còn mở.


### Nghiệm thu TOOL-01d trên server test (2026-09-06)

- Backend/frontend `209d33f`, migration 38, healthy; backup trước migration đã kiểm tra 447290 bytes, 326 TOC lines tại `.cache/deployments/20260906-workflow-checkpoints/database.dump`; image cũ có tag `before-workflow-checkpoints-20260906`.
- Full backend tests với PostgreSQL mới, TypeScript và Docker production build đều qua. Kiểm thử checkpoint bao gồm từ chối dispatch khi chưa lưu được admission, không gọi lại tool đã hoàn tất, bảo toàn input khi resume, chặn runtime đổi/actor khác/unknown effect và chặn writer lease cũ.
- Smoke API xác thực: workflow hai bước ghi; bước đầu hoàn tất, bước hai chờ duyệt; SIGKILL backend rồi restart. Resume giữ input cũ, phát lại kết quả bước đầu từ checkpoint, chỉ tạo xác nhận mới cho bước hai. Endpoint giả lập nhận đúng số lệnh đã duyệt, không ghi trùng.
- Mô phỏng riêng việc mất checkpoint sau ledger succeeded bằng điều chỉnh bản ghi fixture: resume dùng response đã lưu và không dispatch thêm. Đây là fault injection trên dữ liệu test, không phải kiểm thử SAP thật.
- Regression approve/reject chat và workflow, chat FIFO/SSE/replay, MCP discovery/rediscovery/invoke và truy xuất 3 tài liệu thực qua gateway đều qua. Fixture và PostgreSQL kiểm thử tạm đã dọn; server giữ 3 tài liệu/67 chunks, không có phiên chạy/job đang thực thi.
- Còn mở: durable checkpoint/resume cho chat để giải phóng worker khi chờ duyệt; worker workflow tự tiếp tục nền; phê duyệt shared tool; business idempotency/SAP reconciliation và bộ câu hỏi nghiệp vụ nhiều KB được gán nhãn. Chưa nghiệm thu tương tác UI bằng trình duyệt.


### 2026-09-06 — TOOL-01e: chặn vòng lặp xác nhận và giữ việc cần đối soát

- Trước khi tạo yêu cầu xác nhận, kiểm tra ledger chưa kết thúc của đúng actor/workspace/tool/action. Có executing/uncertain thì trả hướng dẫn đối soát; admission vẫn kiểm tra lại dưới khóa để chặn race. Không tự phân loại SAP action là read hoặc gửi lại thao tác chưa rõ kết quả.
- Trong một lượt Chat, action đã bị từ chối/hết hạn/lỗi sau xác nhận hoặc bị policy chặn được loại khỏi danh sách đưa cho model. Backend cũng chặn nếu model vẫn gửi tên đó hoặc đổi tham số. Tool đọc khác vẫn được dùng. Lượt mới có thể yêu cầu lại sau từ chối; ledger unresolved vẫn chặn xuyên lượt.
- Mỗi lần gọi cùng action có attempt riêng trong run_steps, sửa lỗi unique key khiến tool đọc ở vòng tiếp theo thất bại trước khi dispatch.
- API giữ toàn bộ yêu cầu pending/approved/uncertain trong phạm vi được phép cùng 50 bản ghi mới nhất. Kết quả ledger quyết định trạng thái, nên lịch sử mới không che mất yêu cầu cũ cần đối soát.
- Full backend trên PostgreSQL mới qua. Integration model cố gọi cùng action hai lần trong một batch và lặp lại ở vòng sau với tham số khác: đúng một approval, từ chối không dispatch, duyệt rồi HTTP 503 chỉ một dispatch; tool đọc chạy cả hai vòng. Ca 60 receipt mới không che operation cũ và preflight không phát sinh approval cũng qua.
- Đây chưa phải durable chat checkpoint hay giải phóng worker đang chờ duyệt; các phần đó vẫn còn mở.


### 2026-09-06 — TOOL-01f: lưu điểm chờ xác nhận Chat và trả worker

- Migration 40 lưu checkpoint gồm history đã chuẩn bị, citations, kết quả tool trước đó, vị trí trong batch/vòng tool, nội dung đã phát, bộ đếm và deadline ban đầu. Trạng thái waiting_approval, checkpoint và SSE xác nhận commit nguyên tử trước khi executor trả worker. Nội dung không vượt 3 MiB ở admission; không lưu credential cấu hình tool/model trong checkpoint.
- Worker xử lý hội thoại khác trong lúc chờ; câu hỏi sau cùng hội thoại vẫn giữ FIFO. Quyết định chỉ lưu consent, worker nhận lease mới rồi tiếp tục với cùng approval/idempotency key. Restart khi đang parked giữ nguyên yêu cầu đến thời hạn; hết hạn/từ chối đưa lỗi tool vào câu trả lời, không dispatch. Deadline không được kéo dài qua các lần chờ.
- Resume kiểm tra quyền hội thoại/workspace, fingerprint runtime, nguồn KB và tệp đính kèm trước dùng lại history. Tool dùng tham số và definition đã duyệt; admission kiểm tra lại chính sách/owner/ledger. Các callback model sau resume tiếp tục số attempt, không mất accounting.
- Chỉ điểm parked được tiếp tục tự động. Sau khi worker nhận lại lease, crash vẫn chuyển interrupted và không replay vì có thể đã dispatch. Checkpoint của lượt terminal được dọn, ledger/receipt giữ nguyên để đối soát.
- Full backend trên PostgreSQL mới qua. Integration một worker chứng minh không bị chiếm lúc chờ, FIFO, dừng/khởi động lại, hai lần duyệt liên tiếp không lặp lệnh trước, từ chối/hết hạn, đổi cấu hình, thu hồi quyền, hủy và crash sau claim đều qua.
- Chưa triển khai lên server test tại commit này. Phần tiếp theo bổ sung hiển thị điểm chờ khi tải lại trang rồi nghiệm thu rollout; đây chưa phải phục hồi mọi giai đoạn LLM/read/write bị ngắt.
