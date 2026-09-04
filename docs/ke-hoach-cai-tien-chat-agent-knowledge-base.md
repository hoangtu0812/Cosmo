# Kế hoạch cải tiến Chat, Agent và Knowledge Base

Ngày lập: 2026-09-05  
Trạng thái: Kế hoạch đề xuất, chưa triển khai  
Phạm vi: Các vấn đề đã xác định trong đợt rà soát kiến trúc chat, agent và tìm kiếm nhiều Knowledge Base của Cosmo.

## 1. Mục tiêu và kết luận hiện trạng

Cosmo đã có nền phù hợp để tiếp tục phát triển: Go modular monolith, chat và agent dùng chung pipeline, Model Gateway, tool registry, agent/tool versioning, Run/Step/Event và RAG service riêng.

Tuy nhiên, việc có các thành phần này chưa đồng nghĩa luồng sử dụng đã bảo đảm độ tin cậy. Chat vẫn chạy trong HTTP request; Run chủ yếu ghi nhận hoạt động. Tìm kiếm nhiều KB trong chat gọi từng KB rồi so sánh trực tiếp điểm trả về, chưa dùng đầy đủ cơ chế gộp và xếp hạng chung của RAG service.

Kế hoạch này nhằm đạt bốn kết quả:

1. Chat giữ đúng câu hỏi và ngữ cảnh, xử lý được retry, mất kết nối và gửi đồng thời.
2. Agent chạy đúng bản phát hành, đúng quyền, có thể hủy và kiểm soát tác động của tool.
3. Tìm kiếm nhiều KB lấy đúng và đủ bằng chứng, chịu được lỗi từng nguồn và hỗ trợ cấu hình embedding khác nhau.
4. Chất lượng, độ trễ và chi phí được đo trên chính đường thực thi mà người dùng sử dụng.

Đây là kế hoạch củng cố nền hiện có. Không yêu cầu chuyển sang microservices hoặc thay framework agent/RAG.

## 2. Bằng chứng và giới hạn đánh giá

Các vị trí chính đã rà soát:

| Thành phần | Mã nguồn |
|---|---|
| Chat, tạo message/run, history, streaming | `backend/internal/httpapi/server.go` |
| Planner, tool rounds, context | `backend/internal/httpapi/chat_plan.go`, `chat_tools.go`, `chat_context.go` |
| Agent draft, publish và runtime | `backend/internal/agents/repository.go`, `versions.go`; `backend/internal/httpapi/agents.go` |
| Tool invocation và pin phiên bản | `backend/internal/tools/invoke.go`, `agent.go`, `versions.go` |
| Run state machine và worker | `backend/internal/runs/`; `backend/cmd/server/main.go` |
| Chat SSE client | `frontend/app/lib/api.ts`, `frontend/app/chat/page.tsx` |
| Chọn KB, tìm kiếm và gộp kết quả | `backend/internal/httpapi/knowledge_documents.go`, `knowledge.go` |
| Retrieval, vector store, evaluation | `rag-service/app/retrieve.py`, `store.py`, `evaluate.py` |

Kết quả kiểm tra tại thời điểm rà soát:

- `go test ./...` chạy qua. Các bài integration database/MCP được kiểm tra riêng bị skip vì thiếu cấu hình test.
- Bộ test RAG trong container: **92 passed**. Đã đối chiếu mã Python của app và tests trong container với workspace, kết quả khớp.
- Chưa đo tải, độ trễ thực tế hoặc chất lượng trả lời trên bộ câu hỏi nghiệp vụ đã gán nhãn.
- Evaluation hiện gọi trực tiếp Python retrieval, khác đường chat gọi từng KB và gộp kết quả ở Go. Không dùng kết quả đó để kết luận chất lượng chat nhiều KB.

Tài liệu này bổ sung cho `cosmo_agent_workflow_roadmap.md`. Những mô tả trước đây như “durable worker đã có” phải được phân biệt với việc chat đã thực sự chạy qua worker và phục hồi được.

## 3. Nguyên tắc thiết kế

- Giữ Go modular monolith; tách orchestration khỏi HTTP handler thành service có thể kiểm thử độc lập.
- Phân biệt dữ liệu hội thoại, execution state, operational events và dữ liệu đánh giá. Nội dung nhạy cảm không tự động sao chép sang log/trace.
- Phiên bản mô tả hành vi được ghim; quyền truy cập và trạng thái thu hồi credential vẫn được kiểm tra ở thời điểm thực thi.
- Một lượt chat có danh tính ổn định. Retry cùng lượt không tạo thêm message, run hoặc tác động nghiệp vụ.
- Queue có thể giao lại job. Không tuyên bố exactly-once đối với hệ thống ngoài nếu chưa có idempotency hoặc reconciliation tương ứng.
- Điểm từ các retriever/reranker khác nhau không được mặc định xem là cùng thang đo.
- KB lỗi, KB không có kết quả và KB không được phép đọc là ba trạng thái khác nhau.
- Câu hỏi nội bộ thiếu bằng chứng phải được trả lời rõ mức độ thiếu; không tự lấp khoảng trống bằng suy đoán.
- Mọi thay đổi retrieval phải được đánh giá qua đường thực thi dùng trong chat.

## 4. Danh sách ưu tiên

P0 là điều kiện cần trước khi mở rộng sử dụng production; P1 hoàn thiện nền thực thi; P2 tối ưu sau khi có số liệu. Đây là mức ưu tiên của kế hoạch, không phải thang severity của báo cáo lỗi.

| ID | Ưu tiên | Vấn đề | Kết quả cần đạt |
|---|---|---|---|
| CHAT-01 | P0 | History lấy 40 tin đầu, có thể bỏ câu hỏi mới | Lấy đúng các lượt gần nhất, luôn giữ câu hỏi hiện tại |
| CHAT-02 | P0 | Chưa chống gửi trùng và gửi đồng thời ở backend | Idempotency, thứ tự lượt và transaction rõ ràng |
| AGT-01 | P0 | Lưu draft trước khi kiểm tra revision | Conflict không thay đổi dữ liệu |
| AGT-02 | P0 | Người dùng agent có thể yêu cầu chạy draft | Tách quyền test draft và dùng release |
| AGT-03 | P0 | Published agent có thể dùng tool draft | Dependency được pin đầy đủ, thiếu version phải báo lỗi |
| KB-01 | P0 | So sánh điểm khác thang đo giữa các KB | Hợp nhất kết quả bằng cơ chế xếp hạng thống nhất |
| KB-02 | P0 | Tìm tuần tự, một KB lỗi làm mất cả kết quả | Fan-out có giới hạn, kết quả một phần và deadline |
| KB-03 | P0 | Retrieval rỗng/lỗi vẫn sinh câu trả lời thiếu ràng buộc | Chính sách bằng chứng và trả lời thiếu dữ liệu |
| RUN-01 | P1 | HTTP request quyết định vòng đời chat | Runtime qua worker, checkpoint, phục hồi và cancel |
| TOOL-01 | P0 trước tool ghi | Tool ghi chạy trực tiếp, thiếu kiểm soát tác động | Policy, approval khi cần và idempotency |
| KB-04 | P1 | Câu nối tiếp và câu đa nguồn chưa được xử lý đủ | Query rewrite, chia câu hỏi và kiểm tra độ bao phủ |
| KB-05 | P0 khi dùng khác embedding | Một collection chỉ có một kích thước vector | Index theo embedding profile, migration an toàn |
| KB-06 | P1 | Version KB chưa phải snapshot | Phân biệt live knowledge và snapshot release |
| OBS-01 | P1 | Usage chỉ tính generation cuối | Usage và latency của toàn bộ lượt |
| EVAL-01 | P0 cho nghiệm thu | Evaluation khác đường chat thực tế | Bộ kiểm thử và đánh giá bám runtime production |

## 5. Giai đoạn A — Sửa tính đúng đắn của Chat và Agent

### A1. History và ngân sách ngữ cảnh — CHAT-01

Thực hiện:

- Thay truy vấn lấy 40 tin đầu bằng truy vấn các lượt gần nhất, sau đó khôi phục thứ tự hội thoại.
- Xác định history theo lượt đang thực thi, không lấy nhầm câu hỏi được gửi sau lượt đó.
- Có thứ tự ổn định khi timestamp trùng; ưu tiên sequence của message/turn.
- Giữ nguyên cặp assistant tool-call/tool-result khi đưa lịch sử công cụ vào context.
- Dành ngân sách cho system instructions, câu hỏi mới và output; phân bổ phần còn lại cho history, memory, tài liệu, file và tool output.
- Chỉ thêm tóm tắt lịch sử khi cần. Lưu phạm vi message đã tóm tắt để tránh mất hoặc lặp nội dung.

Nghiệm thu:

- Hội thoại vượt 40 và 100 message vẫn gửi đúng câu hỏi hiện tại vào model.
- Không đưa message tương lai hoặc tool-result thiếu tool-call tương ứng vào prompt.
- Khi vượt ngân sách, hệ thống thu gọn có thứ tự hoặc trả lỗi rõ ràng; không âm thầm bỏ câu hỏi mới.

### A2. Danh tính lượt, idempotency và thứ tự thực thi — CHAT-02

Thực hiện:

- Client gửi `client_message_id` ổn định cho một lần submit và giữ nguyên ID khi retry.
- Ràng buộc uniqueness trong phạm vi conversation; cùng ID nhưng payload khác phải bị từ chối.
- Trong một transaction: xác thực lượt, tạo user message, gắn attachment, tạo run và enqueue job khi đường worker được bật.
- Mặc định một conversation chỉ thực thi một lượt tại một thời điểm. Lượt đến sau được xếp hàng theo sequence.
- Request trùng trả lại danh tính và trạng thái lượt cũ, không tạo execution mới.
- Tránh giữ transaction database mở trong suốt thời gian gọi model/tool.

Nghiệm thu:

- Retry cùng request tạo đúng một user message và một run.
- Hai tab gửi đồng thời được xử lý theo thứ tự đã xác định.
- Lỗi giữa transaction không để message, attachment và run lệch trạng thái.

### A3. Draft, quyền và release — AGT-01/02/03

Thực hiện:

- Kiểm tra revision và cập nhật draft, KB bindings, tool bindings trong cùng transaction.
- Bỏ đường bypass revision không được kiểm soát; client phải gửi revision đã đọc.
- Chỉ người có quyền chỉnh sửa được tạo và tiếp tục conversation chạy draft.
- Luồng sử dụng thông thường bắt buộc có published release; chưa publish phải báo rõ, không fallback sang draft.
- Khi publish, kiểm tra đầy đủ dependency và pin exact tool version.
- Không fallback sang tool draft nếu dependency đã pin bị thiếu. Với agent cũ, cung cấp bước rà soát và phát hành lại.
- Ghi agent version, tool versions và cấu hình model liên quan vào execution snapshot của run; không lưu plaintext credential.
- Re-authorize quyền sử dụng khi thực thi, bao gồm sau thời gian chờ queue hoặc approval.

Nghiệm thu:

- Hai editor cùng sửa: request thua nhận conflict và không làm thay đổi dữ liệu.
- Người chỉ có quyền dùng agent không thể chạy draft qua crafted request hoặc conversation draft cũ.
- Sửa tool draft không làm thay đổi agent release đã pin.
- Mỗi run truy được exact agent/tool version đã dùng.

## 6. Giai đoạn B — Runtime bền vững và kiểm soát Tool

### B1. Tách vòng đời execution khỏi HTTP — RUN-01

Luồng đích:

```text
Submit turn
  -> Authorize + transaction message/run/job
  -> Worker claim + lease
  -> Resolve execution snapshot
  -> Plan / retrieve / model / tool steps
  -> Persist checkpoint và message output
  -> Finalize run

Browser
  -> Theo dõi trạng thái và nội dung qua SSE
  -> Reconnect bằng cursor
  -> Gửi yêu cầu cancel
```

Thực hiện:

- Tạo service orchestration dùng chung cho chat thường và agent; HTTP chỉ tiếp nhận, kiểm tra quyền và stream.
- Đăng ký worker handler cho chat; bảo đảm run creation failure chặn execution thay vì coi là lỗi telemetry có thể bỏ qua.
- Persist checkpoint tại ranh giới step. Lưu output theo batch hợp lý và có trạng thái partial/completed.
- Dùng event sequence hoặc output cursor để reconnect không phải gửi lại câu hỏi.
- Gửi `run_id` ngay sau khi chấp nhận lượt, trước planning/retrieval/tool.
- Cancel truyền tới context model/tool đang chạy và được kiểm tra trước step tiếp theo.
- Phân biệt `cancelled`, `timed_out`, `failed`, `succeeded`; chỉ phát hoàn tất khi trạng thái và output đã được ghi nhất quán.
- Worker mất lease phải ngừng thực thi; dùng fencing/attempt identity để worker cũ không ghi đè kết quả worker mới.
- Xử lý job hết số lần thử, run bị bỏ dở và shutdown trong quá trình chạy.
- Không tự động gọi lại tool có tác động chưa xác định sau crash; chuyển sang bước kiểm tra kết quả hoặc yêu cầu xử lý.
- Frontend hỗ trợ Stop, reconnect và trạng thái partial. EOF trước terminal event không được xem là thành công.
- Kiểm tra kết thúc stream ở Model Gateway; không coi luồng bị cắt hoặc phản hồi sai định dạng là câu trả lời hoàn chỉnh.

Nghiệm thu:

- Reload hoặc mất mạng không tạo thêm run; khi kết nối lại, UI thấy đúng trạng thái và nội dung đã lưu.
- Restart worker giữa hai step tiếp tục đúng checkpoint.
- Cancel trong lúc retrieval/model/tool làm dừng phần công việc còn có thể hủy và chặn step mới.
- Hai worker không cùng finalize một attempt; worker mất lease không ghi đè execution mới.
- Tool đã có tác động bên ngoài trước khi cancel vẫn được ghi nhận, không hiển thị như đã được hoàn tác.

### B2. Policy và tác động bên ngoài — TOOL-01

Thực hiện:

- Khai báo capability đọc/ghi, mức tác động, timeout, giới hạn output và hỗ trợ idempotency theo action.
- Áp dụng policy của workspace/agent tại server trước mỗi invocation. Metadata từ MCP server là đầu vào tham khảo, không tự cấp quyền.
- Validate arguments theo schema tại runtime, gồm required fields, kiểu, enum và giới hạn kích thước.
- Với action cần approval: lưu payload chuẩn hóa, hash, người được duyệt và thời hạn; payload đổi phải duyệt lại.
- Resolve credential tại thời điểm gọi; giữ kiểm soát egress hiện có.
- Lưu tool execution ID, attempt, idempotency key và kết quả đủ để đối soát.
- Chuẩn hóa HTTP status và lỗi nghiệp vụ; không đánh dấu thành công chỉ vì HTTP client nhận được response.
- Tool output và tài liệu là dữ liệu không tin cậy; không được tự mở rộng quyền hoặc thay chỉ dẫn hệ thống.

Nghiệm thu:

- Model không gọi được tool/action ngoài capability được cấp.
- Payload sau approval không thể đổi âm thầm; quyền bị thu hồi có hiệu lực trước invocation.
- Crash sau khi hệ thống ngoài đã nhận request không tạo tác động trùng khi retry.
- Action không có bảo đảm idempotency dùng reconciliation hoặc xử lý thủ công khi kết quả chưa xác định.

## 7. Giai đoạn C — Tìm kiếm nhiều Knowledge Base

### C1. Một orchestration retrieval dùng chung — KB-01/02

Luồng đích:

```text
Câu hỏi + lịch sử liên quan
  -> Câu hỏi tìm kiếm độc lập / các câu hỏi thành phần
  -> Resolve KB allow-list và index version
  -> Fan-out theo KB hoặc nhóm embedding profile
  -> Gom ứng viên, giữ provenance và thứ hạng cục bộ
  -> Fusion + rerank chung theo policy
  -> Loại trùng + kiểm tra độ bao phủ nguồn
  -> Đóng gói bằng chứng trong ngân sách token
  -> Sinh câu trả lời và citation
```

Thực hiện:

- Giữ quyết định quyền ở Go; các request tới RAG nhận allow-list cụ thể.
- Xác lập một contract retrieval chung cho chat, màn hình test KB và evaluation.
- Truy vấn song song có giới hạn; đặt deadline tổng, timeout riêng từng KB và cancellation.
- Tái sử dụng query embedding cho KB dùng cùng gateway/model/index profile. Không dùng chung vector giữa những profile không tương thích.
- Trả trạng thái từng KB: thành công có kết quả, thành công rỗng, lỗi hoặc timeout. Không tiết lộ KB ngoài quyền truy cập.
- Giữ kết quả từ nguồn đã thành công; không bỏ toàn bộ vì một nguồn lỗi.
- Không sort trực tiếp điểm từ reranker khác nhau hoặc trộn rerank score với RRF score.
- Dùng fusion theo thứ hạng để gom ứng viên, sau đó rerank chung nếu policy cho phép gửi các đoạn tới cùng provider.
- Nếu không được rerank chung, dùng cơ chế fusion theo rank và công bố giới hạn chất lượng; không gửi dữ liệu sang provider ngoài phạm vi được phép.
- Giới hạn số ứng viên theo ngân sách toàn lượt, có độ bao phủ cần thiết trước rerank.

Nghiệm thu:

- KB bật/tắt rerank hoặc dùng reranker khác nhau không bị ưu tiên chỉ do thang điểm lớn hơn.
- Một KB timeout vẫn giữ được kết quả hợp lệ của KB còn lại, kèm trạng thái nguồn thiếu.
- Số request đồng thời không vượt cấu hình; tổng lượt tuân thủ deadline.
- KB bị thu hồi quyền không xuất hiện trong truy vấn, prompt hoặc citation của lượt mới.

### C2. Hiểu câu nối tiếp và lấy đủ bằng chứng — KB-04

Thực hiện:

- Viết lại câu hỏi nối tiếp thành câu hỏi độc lập bằng lịch sử gần nhất và nguồn đã tham chiếu.
- Tách câu hỏi đối chiếu thành các phần cần bằng chứng; tránh thêm giả định không có trong lời người dùng.
- Planner nhận tên/mô tả KB và chỉ được chọn trong allow-list. Khi không chắc nguồn, mở rộng tìm trong phạm vi được phép.
- Gộp kết quả theo câu hỏi thành phần, loại trùng xuyên KB và đa dạng theo tài liệu/nguồn.
- Không áp quota cứng buộc mọi KB phải xuất hiện; chỉ giữ nguồn liên quan và kiểm tra đủ bằng chứng cho các phần được hỏi.
- Thay `globalLimit = max(perKBTopK)` bằng cấu hình retrieval budget cấp lượt: số ứng viên, token bằng chứng và output dự kiến.
- Kiểm tra chất lượng dedup để không loại mất đoạn có cùng phần mở đầu nhưng khác nội dung quan trọng.

Nghiệm thu:

- “Quy định đó áp dụng khi nào?” giữ đúng chủ thể từ lượt trước.
- Câu hỏi cần KB A và KB B lấy được bằng chứng của cả hai khi dữ liệu có sẵn.
- KB lớn hoặc nhiều bản tài liệu trùng không chiếm hết context.
- Có bài test phân biệt nguồn không liên quan với nguồn liên quan nhưng thiếu kết quả.

### C3. Thiếu bằng chứng, mâu thuẫn và citation — KB-03

Thực hiện:

- Luôn chuyển trạng thái retrieval vào lớp quyết định câu trả lời, kể cả khi không có passage.
- Phân biệt: không tìm thấy dữ liệu; nguồn không truy cập được; dữ liệu chỉ trả lời một phần; nguồn mâu thuẫn.
- Nếu câu hỏi cần quy định nội bộ nhưng không có bằng chứng, trả lời rõ giới hạn hoặc hỏi thêm; không tự suy ra quy định từ kiến thức chung.
- Chỉ cho phép câu trả lời một phần khi phần đó có bằng chứng; nêu phần chưa xác minh.
- Gắn citation với document/chunk/index version đủ để kiểm tra lại; không chỉ kiểm tra cú pháp dấu trích dẫn.
- Đánh giá ngưỡng relevance sau rerank trên dữ liệu thật, không dùng một ngưỡng chung tùy ý cho mọi loại score.
- Khi có nguồn mâu thuẫn, dùng metadata hiệu lực/phiên bản và chính sách nguồn chuẩn; không mặc định tài liệu mới hơn luôn đúng hơn.

Nghiệm thu:

- Không có kết quả hoặc tất cả KB lỗi không tạo câu trả lời khẳng định quy định nội bộ không có nguồn.
- Câu trả lời từng phần nói rõ giới hạn và chỉ dẫn nguồn cho phần đã xác minh.
- Các citation tồn tại, người dùng được phép mở và hỗ trợ nội dung được trích.

## 8. Giai đoạn D — Index và vòng đời dữ liệu KB

### D1. Embedding profile và re-index an toàn — KB-05

Thực hiện:

- Định nghĩa embedding profile gồm provider/gateway identity, model revision, dimensions và cấu hình tiền xử lý liên quan. Không chứa API key.
- Lưu profile/index generation đã dùng khi ingest; retrieval dùng đúng profile đó thay vì đọc model mới nhất một cách mù quáng.
- Tách collection hoặc named vector phù hợp theo profile; không dùng một dense vector dimension cho mọi model.
- Đổi embedding/chunking tạo index generation mới, không phá index đang phục vụ.
- Backfill, xác minh, đánh giá rồi chuyển active index pointer. Giữ generation trước để rollback trong thời hạn xác định.
- Re-index một KB không reset collection chứa dữ liệu của KB khác.
- Thay dữ liệu index theo cơ chế staging/activation; tránh xóa bản đang hoạt động trước khi bản mới ghi thành công.

Nghiệm thu:

- Hai KB dùng vector khác dimensions vẫn ingest và search đúng qua profile riêng.
- Đổi sang model cùng dimensions nhưng khác không gian embedding không âm thầm tìm trên index cũ.
- Re-index lỗi giữ nguyên dữ liệu đang phục vụ; rollback không phải ingest lại từ đầu.

### D2. Live knowledge và snapshot release — KB-06

Thực hiện:

- Quy định rõ hai chế độ: `live` đọc dữ liệu đang hoạt động; `snapshot` đọc release cố định.
- Snapshot ghi manifest document versions, chunk/index references và metadata hiệu lực.
- Workspace installation và agent release pin snapshot nếu nghiệp vụ yêu cầu tái lập kết quả.
- Kiểm tra quyền hiện hành kể cả khi snapshot còn tồn tại; pin version không vượt quyền truy cập.
- Không coi `installed_version` hiện có là snapshot lịch sử có thể tái tạo. Dữ liệu hiện tại cần được đánh dấu live hoặc tạo snapshot mới từ trạng thái đã xác minh.

Nghiệm thu:

- Cập nhật tài liệu không làm đổi snapshot đã phát hành.
- Hai run trên hai phiên bản KB có thể truy ra đúng tài liệu đã dùng.
- UI thể hiện rõ đang dùng live hay phiên bản cố định.

## 9. Giai đoạn E — Hiệu năng, chi phí và đánh giá chất lượng

### E1. Model loop và usage toàn lượt — OBS-01

Thực hiện:

- Chuẩn hóa model result: text, tool calls, finish reason và usage trong một contract.
- Khi model đã trả lời và không cần tool, dùng kết quả đó; tránh bỏ đi rồi gọi generation lần nữa.
- Theo dõi usage cho planning, query rewrite, tool decision, generation, title, suggestions, memory, embedding và reranking theo khả năng provider cung cấp.
- Giữ trạng thái “usage không có” khác với 0; không giả lập số token hoặc chi phí là số đo thật.
- Gắn model calls và retrieval/tool calls với run/step; ghi thời gian queue, first token, từng KB và toàn lượt.
- Đưa title/suggestions/memory ra khỏi đường chặn hoàn tất câu trả lời; nếu chạy nền thì có job và trạng thái riêng.
- Thay giới hạn chỉ đếm tool rounds bằng ngân sách tổng: số calls, token, thời gian và phát hiện lặp. Chỉ retry lỗi phù hợp.

Nghiệm thu:

- Một lượt không cần tool không bị gọi thêm model chỉ để viết lại câu trả lời đã có.
- Usage có thể truy đến từng model call; tổng lượt khớp các call đã ghi nhận.
- Title/suggestions chậm không giữ trạng thái câu trả lời ở “đang xử lý”.

### E2. Evaluation qua đường chat thực tế — EVAL-01

Thực hiện:

- Xây bộ câu hỏi gán nhãn bằng tài liệu gốc, không lấy kết quả hệ thống hiện tại làm đáp án đúng.
- Giữ unit test cho thuật toán; bổ sung integration test qua retrieval orchestration của Go và end-to-end chat/agent.
- Cho evaluator hỗ trợ câu không có đáp án, nguồn bị cấm, nguồn lỗi và lịch sử nhiều lượt.
- Ghi exact agent/tool/KB/index version và model configuration cho mỗi báo cáo.
- Đo recall/precision/MRR/nDCG, độ bao phủ bằng chứng đa nguồn, citation correctness, tỷ lệ trả lời thiếu căn cứ, p50/p95 latency và usage.
- Báo tỷ lệ lỗi riêng và đánh giá trải nghiệm tổng thể; không để việc loại câu lỗi khỏi trung bình che khuất tình trạng hệ thống.

Bộ tình huống tối thiểu:

| Nhóm | Tình huống |
|---|---|
| History | Vượt 40/100 message, câu nối tiếp, nhiều tab |
| Multi-KB | Một nguồn, hai nguồn bắt buộc, nhiều KB không liên quan |
| Ranking | Trộn rerank on/off, khác reranker, KB lớn/nhỏ, tài liệu trùng |
| Failure | Một KB lỗi, tất cả KB lỗi, gateway chậm, stream đứt, worker restart |
| Authorization | KB bị thu hồi, người dùng mất membership, draft trái quyền |
| Version | Tool draft đổi, thiếu pinned version, live/snapshot KB |
| Index | Embedding khác dimensions, đổi model cùng dimensions, re-index lỗi |
| Evidence | Không có đáp án, nguồn mâu thuẫn, citation sai, tài liệu chứa chỉ dẫn gây nhiễu |
| Side effects | Retry submit, retry tool, crash sau outbound request, cancel khi đang gọi |

Trước rollout phải chốt ngưỡng nghiệm thu từ baseline và nhu cầu sản phẩm. Chưa có dữ liệu để cam kết con số độ trễ hoặc recall cụ thể trong kế hoạch này. Vi phạm quyền, duplicate side effect và mất câu hỏi hiện tại là lỗi chặn phát hành, không được bù bằng điểm trung bình tốt.

## 10. Thứ tự triển khai và các cổng nghiệm thu

| Mốc | Phạm vi | Điều kiện hoàn thành |
|---|---|---|
| M0 — Baseline | EVAL-01 phần test harness, thống nhất contract | Có test tái hiện lỗi và số liệu ban đầu trên đường chat |
| M1 — Tính đúng đắn | Giai đoạn A; KB-03 | Không mất câu hỏi, không ghi đè draft, không bypass release, trả lời thiếu bằng chứng đúng chính sách |
| M2 — Execution | Giai đoạn B; OBS-01 nền | Retry/cancel/reconnect/restart có test; tool ghi chỉ mở khi policy và idempotency đạt |
| M3 — Multi-KB | Giai đoạn C; EVAL-01 đa nguồn | Gộp đúng thang đo, giữ kết quả một phần, đủ nguồn cho câu hỏi tổng hợp |
| M4 — Vòng đời KB | Giai đoạn D | Đa embedding, re-index an toàn và live/snapshot rõ ràng |
| M5 — Rollout | Giai đoạn E và kiểm thử tổng thể | Không hồi quy quyền/tính đúng đắn; chất lượng, latency, usage đạt ngưỡng đã duyệt |

KB-05 phải hoàn thành trước khi cho phép triển khai nhiều embedding profile không tương thích, dù thứ tự rollout các mốc khác có thay đổi. TOOL-01 phải hoàn thành trước khi mở action ghi nghiệp vụ quan trọng.

Chưa chốt thời lượng vì chưa có thông tin nhân sự và dữ liệu vận hành. Khi lập sprint, chia mỗi mục theo thay đổi schema/contract, backend, frontend, kiểm thử và migration; không nghiệm thu chỉ bằng việc giao diện đã xuất hiện.

## 11. Migration và rollout

1. Bổ sung schema tương thích ngược: turn identity/sequence, execution snapshot, checkpoint, tool execution, embedding profile và index generation theo từng mốc.
2. Backfill có kiểm tra. Không gán giả phiên bản cho run cũ hoặc biến version KB hiện tại thành snapshot lịch sử chưa từng lưu.
3. Bật runtime/retrieval mới theo workspace. Một lượt đã bắt đầu giữ nguyên execution path; không chạy đồng thời hai runtime cho cùng lượt.
4. Chỉ shadow retrieval đọc dữ liệu trong phạm vi cho phép. Không shadow tool ghi. Theo dõi chi phí tăng do shadow.
5. So sánh baseline/candidate trên cùng bộ câu hỏi, dữ liệu và điều kiện model; pilot trước khi mở rộng.
6. Rollback bằng feature flag cho lượt mới và active index pointer. Các run đang thực thi cần được drain, cancel hoặc tiếp tục bằng runtime tương thích.
7. Dọn dữ liệu cũ sau retention đã thống nhất; không xóa phiên bản/index còn được snapshot hoặc run cần giữ tham chiếu.

## 12. Checklist hoàn thành

- [ ] Chat không bỏ câu hỏi mới hoặc trộn thứ tự các lượt đồng thời.
- [ ] Submit retry và tool retry không tạo tác động trùng.
- [ ] Draft conflict không ghi dữ liệu; draft execution yêu cầu đúng quyền.
- [ ] Agent release pin dependency và run lưu đúng snapshot.
- [ ] Cancel, reconnect, timeout và worker recovery đạt integration test.
- [ ] Multi-KB không so sánh trực tiếp các loại score khác thang đo.
- [ ] Một KB lỗi không làm mất kết quả hợp lệ còn lại.
- [ ] Câu hỏi đa nguồn và câu nối tiếp có kiểm thử chất lượng.
- [ ] Thiếu bằng chứng hoặc nguồn mâu thuẫn được thể hiện đúng.
- [ ] Đa embedding và re-index không phá dữ liệu đang phục vụ.
- [ ] Live KB và snapshot KB có semantics rõ ràng.
- [ ] Usage/latency phản ánh toàn bộ lượt và các call phụ.
- [ ] Evaluation chạy qua cùng orchestration với chat.
- [ ] Có baseline, báo cáo candidate, migration và rollback đã kiểm thử.
- [ ] Tài liệu hiện trạng được cập nhật theo hành vi đã xác minh, không theo thành phần mới chỉ có scaffolding.
