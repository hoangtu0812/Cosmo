# Kế hoạch cải tiến Chat, Agent và Knowledge Base

Ngày lập: 2026-09-05  
Trạng thái: Đang triển khai tuần tự; tiến độ chi tiết tại mục 13
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

## 13. Nhật ký triển khai

### 2026-09-05 — CHAT-01a: History gần nhất và snapshot câu hỏi

- Hoàn thành phần sửa lỗi lấy 40 tin đầu: lấy tối đa 39 tin gần nhất và luôn thêm câu hỏi hiện tại ở cuối.
- Lưu câu hỏi và đọc history trong cùng một PostgreSQL statement snapshot, trước planning/retrieval; lượt đến sau không được đọc lại vào context của lượt hiện tại.
- Thứ tự khi timestamp trùng ổn định theo ID; câu hỏi hiện tại không phụ thuộc thứ tự ID này.
- Thêm integration tests trên PostgreSQL thật cho 0/40/100 tin cũ, timestamp trùng, cách ly conversation và lưu câu hỏi đúng một lần.
- Kiểm tra: integration tests mới và `go test ./...` đều qua trên database test riêng đã migrate; các integration database hiện có cũng được chạy.
- CHAT-01 còn phần ngân sách context và quản lý lịch sử tool; CHAT-02 còn idempotency và thứ tự các lượt đồng thời. Thay đổi này chưa giải quyết những phần đó.

### 2026-09-05 — AGT-01: Lưu draft có revision và transaction

- Kiểm tra và khóa revision trước khi thay đổi nội dung; lỗi cập nhật KB hoặc callback gắn tool rollback toàn bộ transaction.
- Revision bắt buộc ở endpoint lưu draft và gắn tool. Client gửi revision khi sửa thông tin agent hoặc gắn tool; autosave không chạy chồng với thao tác gắn tool.
- Giữ trạng thái dirty nếu người dùng gõ thêm trong lúc đang lưu.
- Thêm integration tests cho hai writer cùng revision, rollback KB, rollback binding và từ chối revision thiếu/không hợp lệ.
- Kiểm tra: `go test ./...` với database test riêng, frontend build và TypeScript đều qua. Đã xóa một file validator Next.js sinh cũ trong `.next` không tương thích với route types của Vinext để kiểm tra TypeScript đúng môi trường hiện tại.
- Chưa thay đổi cơ chế pin dependency và quyền chạy draft; xử lý ở AGT-02/03.

### 2026-09-05 — AGT-02: Quyền chạy draft và dùng release

- Người chỉ có quyền dùng agent không được tạo hoặc tiếp tục conversation draft. Quyền sửa được kiểm tra lại mỗi lượt.
- Target mặc định/published yêu cầu release tồn tại; target không hợp lệ bị từ chối, không fallback sang draft.
- Chat kiểm tra lại membership và visibility trước khi đọc runtime; version phải thuộc đúng agent của conversation.
- Integration tests qua HTTP tạo conversation và runtime trên PostgreSQL thật kiểm tra từ chối draft, agent chưa publish, target sai, quyền bị thu hồi và version thuộc agent khác.
- Kiểm tra: `go test ./...` với database integration đều qua.

### 2026-09-05 — AGT-03a: Dependency bắt buộc có phiên bản

- Publish agent kiểm tra tool release và giữ khóa agent/dependency trong transaction; không phát hành agent có tool chưa publish.
- Published runtime từ chối thiếu tool, thiếu pin hoặc không tìm thấy version, thay vì đọc draft. Draft test và release không có tool vẫn được hỗ trợ.
- Chuẩn bị tool set trước khi chấp nhận câu hỏi; lỗi dependency trả về trước message/run/model call.
- Run lưu `resource_version` và map tool versions; seed publish tool trước agent.
- Integration test xác minh sửa action draft không đổi action của release, legacy thiếu pin bị từ chối và tool bị xóa không bị bỏ qua.
- Tương thích: agent release cũ có tool chưa pin sẽ cần quản trị kiểm tra, publish tool và publish lại agent. Không tự gán version cho lịch sử cũ.
- Kiểm tra: `go test ./...` với database integration. Còn mở rộng snapshot metadata mô tả/model và policy thu hồi capability tại từng invocation trong TOOL-01/RUN-01.

### 2026-09-05 — KB-03a: Không suy đoán khi retrieval không có bằng chứng

- Khi planner yêu cầu tra KB mà không nhận được passage, trả lời cố định nêu thiếu bằng chứng; phân biệt không có kết quả với lỗi truy cập nguồn.
- Không chạy tool hoặc model sinh câu trả lời, tạo tiêu đề, gợi ý và memory trong nhánh này. Vẫn lưu câu trả lời và kết thúc SSE bình thường.
- Integration test gọi endpoint chat thật với PostgreSQL và gateway/RAG giả lập, xác minh chỉ có model call của planner và nội dung lưu đúng khi KB rỗng hoặc trả lỗi.
- Kiểm tra: `go test ./...` với database integration đều qua.
- Còn xử lý bằng chứng mâu thuẫn, chất lượng quyết định của planner và cảnh báo khi chỉ truy cập được một phần nguồn; không coi toàn bộ KB-03 đã hoàn thành.

### 2026-09-05 — KB-01a: Gộp theo thứ hạng thay vì điểm khác thang đo

- Gộp các danh sách theo reciprocal rank cục bộ; thứ hạng bằng nhau dùng KB ID để kết quả không phụ thuộc thứ tự response. Giữ nguyên score gốc và ghi rõ `score_scope=per_kb`, `local_rank`, `fusion_score` trong retrieval log.
- Kiểm tra KB của mỗi passage trước fusion và logging; kiểm tra lỗi đọc danh sách quyền từ database.
- Loại passage rỗng và bản trùng hoàn toàn trong cùng document/section/page. So sánh toàn văn bằng hash, không cắt prefix làm mất đoạn có nội dung cuối khác nhau.
- Tests xác minh score 900 không lấn score 0.02 chỉ vì thang đo, thứ tự response không đổi kết quả và đoạn khác nội dung/provenance được giữ. `go test ./...` với database integration đều qua.
- Đây là fallback theo rank khi chưa có policy rerank chung, không phải reranker ngữ nghĩa. Còn dedup xuyên tài liệu/KB có giữ nhiều provenance, budget cấp lượt và evaluation chất lượng; chưa hoàn thành toàn bộ KB-01/04.

### 2026-09-05 — KB-02a: Truy vấn đồng thời, deadline và kết quả một phần

- Fan-out bằng worker pool, mặc định 4 request đồng thời mỗi lượt (trần 16), timeout tổng 30 giây và timeout riêng mỗi KB 10 giây. Hủy context không khởi chạy các request đang chờ.
- Mỗi KB vẫn resolve gateway/embedding riêng của workspace sở hữu; không gửi nội dung sang một provider chung chưa được cho phép.
- Giới hạn passage đầu ra cấp lượt mặc định 24 (trần 100), thay `max(perKBTopK)`; từng request vẫn tôn trọng TopK riêng và không vượt budget cấp lượt. Chưa có budget token hoặc giới hạn tải tổng xuyên nhiều lượt.
- Giữ passage hợp lệ khi nguồn khác lỗi/timeout, ghi `ready/empty/failed/timed_out/canceled` cùng thời gian và số passage từng KB vào output retrieval step. Không lưu lỗi upstream thô có thể chứa dữ liệu nhạy cảm.
- Chat phát trạng thái partial, thêm chỉ dẫn về phần chưa xác minh và lưu thông báo nguồn thiếu ngay trong câu trả lời. Không còn biến kết quả một phần thành thất bại toàn bộ.
- Tests kiểm tra concurrency thực, cancellation, timeout riêng/tổng, nguồn rỗng/lỗi, budget, gateway riêng, quyền bị thu hồi và chặn passage ngoài quyền trước log. Integration qua endpoint chat xác minh câu trả lời/cảnh báo, citation và trạng thái nguồn được lưu.
- Cấu hình mới được mô tả trong `.env.example`. `go test ./...` với database integration đều qua.
- Còn gom request theo embedding profile, tái sử dụng query embedding và nối màn hình test/evaluation vào cùng contract; những phần đó chưa hoàn thành.

### 2026-09-05 — KB-04a: Query cho câu hỏi nối tiếp

- Planner nhận tối đa 6 tin gần nhất/6.000 ký tự Unicode, tên và mô tả KB trong quyền truy cập, cùng tên tệp đính kèm. Không đưa system prompt, memory hoặc tool payload vào lịch sử planner.
- Trong cùng model call lập kế hoạch, trả JSON gồm quyết định tra KB và câu hỏi tìm kiếm độc lập. Chat dùng query này cho retrieval; câu hỏi gốc trong messages được giữ nguyên.
- Không rewrite lượt đầu không có lịch sử. JSON sai, query rỗng/quá dài hoặc gateway lỗi thì dùng câu hỏi gốc và vẫn tra cứu; không đưa lỗi upstream hoặc output planner thô vào SSE.
- Lỗi đọc danh sách KB không còn bị hiểu thành workspace không có KB. Quyền truy cập vẫn được resolve ở retrieval, planner không được chọn hay mở rộng KB ID.
- Unit tests kiểm tra parser/fallback và giới hạn lịch sử; integration qua chat xác minh câu “quy định đó” nhận chủ thể QT-17 từ lịch sử và query đã rewrite được gửi đến RAG. `go test ./...` với database integration đều qua.
- Tests dùng gateway giả lập để xác minh contract/đường đi; chưa chứng minh chất lượng rewrite của model thực tế. Còn câu hỏi thành phần, coverage đa nguồn, dedup xuyên KB và đánh giá chất lượng trên bộ câu hỏi thật.

### 2026-09-05 — CHAT-02a: Tiếp nhận câu hỏi trong một transaction

- User message, history snapshot, attachment claim, tiêu đề ban đầu và run/queued event được commit cùng nhau. Lỗi bất kỳ bước nào rollback toàn bộ, không tiếp tục model/tool khi chưa có run.
- Khóa conversation trong transaction ngắn để thứ tự tiếp nhận và claim tệp không ghi chồng. Không giữ transaction khi gọi model/tool.
- Run repository có `CreateTx` để tham gia transaction của caller; xử lý cạnh tranh idempotency bằng `ON CONFLICT` không làm hỏng transaction.
- Integration PostgreSQL kiểm tra rollback khi tạo run lỗi, attachment claim thiếu, và commit đầy đủ message/attachment/run event. `go test ./...` đều qua với database integration.
- Chưa có client message identity, chống execution đồng thời, queue hay recovery sau crash; các phần này tiếp tục ở CHAT-02/RUN-01.

### 2026-09-05 — CHAT-02b: Idempotency và một execution mỗi hội thoại

- Migration 26 thêm `chat_turns`: uniqueness theo conversation/client_message_id, hash payload content/model/reasoning, sequence tăng và liên kết message/run. Unique partial index chặn hai lượt executing trong cùng hội thoại.
- Cùng ID/payload trả identity và trạng thái lượt cũ; lượt thành công phát lại `meta/done` từ answer đã lưu, không gọi model/tool. Cùng ID khác payload trả 409. Xóa messages không xóa identity nên không làm request cũ chạy lại.
- Lưu answer và trạng thái succeeded trong cùng PostgreSQL statement. Handler thoát trước đó đánh dấu interrupted và giữ identity; không tự retry execution có kết quả tool chưa xác định.
- Shared client tạo ID, giữ qua lỗi mạng/EOF và reload cùng tab bằng sessionStorage (chỉ hash và ID), xóa sau `done`. Cả chat thường và agent test dùng chung; refresh transcript để bỏ bản optimistic trùng và khôi phục ô nhập khi gửi lỗi.
- Client cũ thiếu ID vẫn được nhận bằng ID server sinh, nhưng không có bảo đảm retry. Tệp pending được chụp/claim ở lần tiếp nhận đầu; retry không claim tệp mới. Muốn gửi tệp mới trong lượt mới cần ID mới.
- Kiểm tra: HTTP integration cho duplicate đang chạy/hoàn thành, payload mismatch, conversation bận, người khác truy cập, xóa transcript; tám writer cùng ID chỉ một winner; interrupted không rerun và sequence lượt mới tăng. Backend suite/PostgreSQL, frontend retry tests, build và TypeScript đều qua.
- Giới hạn rollout: drain request trên phiên bản cũ trước khi chuyển backend để mọi writer dùng guard mới. Chưa có queue: lượt khác khi đang bận nhận 409 thay vì xếp hàng. Crash tiến trình có thể để lượt executing chặn hội thoại; cần reconciliation/recovery trong RUN-01 trước rollout yêu cầu tự phục hồi. Không tự reset các lượt này vì tool có thể đã tác động hệ thống ngoài.

### 2026-09-05 — CHAT-02c: Xóa đúng cặp message của lượt

- Xóa message dùng liên kết user/assistant trong `chat_turns`; câu hỏi interrupted chưa có answer không lấy nhầm answer của lượt kế tiếp.
- Legacy chưa có identity vẫn dùng heuristic thời gian, nhưng không ghép với message thuộc lượt đã có identity. Trả/audit đúng các ID thực sự được xóa.
- Khóa conversation trong transaction ngắn và từ chối xóa khi có lượt executing. Giữ turn identity sau xóa để retry không chạy lại tool/model.
- Integration PostgreSQL kiểm tra interrupted, legacy cạnh lượt mới, quyền owner, chặn đang chạy và giữ tombstone. `go test ./...` đều qua với database integration.

### 2026-09-05 — RUN-01a / CHAT-02d: Queue bền vững và worker chat

- Migration 27 bổ sung trạng thái queued, payload/cấu hình fingerprint, danh sách attachment đã chụp, lease và bảng SSE events. Tiếp nhận câu hỏi commit trước khi chờ subscriber; không thực thi model/tool trong HTTP subscriber.
- Server chạy worker chat có giới hạn (`CHAT_WORKERS`, mặc định 2, trần 16). PostgreSQL claim theo sequence, `SKIP LOCKED` và unique executing đảm bảo mỗi hội thoại một lượt; các hội thoại khác có thể chạy song song. Lượt mới được xếp hàng thay vì trả busy 409.
- Worker dựng lại history theo sequence trước khi chạy: giữ answer của lượt trước dù được tạo sau thời điểm câu hỏi đang chờ; loại toàn bộ lượt tương lai. Transcript cũng hiển thị theo cặp user/assistant của sequence.
- Worker kiểm tra lại membership/agent access/dependency. Model, gateway, instruction hoặc tool configuration thay đổi khi đang chờ thì từ chối chạy với cấu hình khác; không lưu credential trong payload. Attachment chỉ đọc danh sách đã chụp lúc tiếp nhận.
- Lease được gia hạn và theo dõi cancellation; mất lease/hủy run dừng context và được kiểm tra trước invocation, trước lưu answer. Answer chỉ được commit khi worker còn sở hữu lease và run còn running. `CHAT_EXECUTION_TIMEOUT` mặc định 10 phút giới hạn cả lượt.
- Queued turn được worker mới tiếp tục sau restart. Executing turn hết lease được đánh dấu interrupted cùng run/step; không tự chạy lại tool có tác động chưa xác định. Answer đã lưu là checkpoint để khôi phục run succeeded nếu tiến trình chết trước khi hoàn tất telemetry.
- SSE được lưu trước khi phát; subscriber/retry đọc lại cùng lượt, hỗ trợ `Last-Event-ID`. Đóng kết nối không hủy worker. Xóa transcript bị chặn khi còn queued/executing.
- Tests PostgreSQL/HTTP qua worker thật trong process kiểm tra FIFO, nhiều subscriber, lịch sử/transcript đúng thứ tự, ngắt subscriber, membership bị thu hồi, cấu hình thay đổi, cancellation tới gateway, replay cursor. Mô phỏng worker chết bằng lease hết hạn xác minh worker cũ bị chặn ghi và worker mới nhận lượt chờ; chưa chạy bài chaos kill container ngoài môi trường test.
- `go test ./...` với database integration qua. Rollout cần drain backend cũ trước migrate/start phiên bản này. Còn resume từng checkpoint model/tool, reconciliation action ghi, retention/compaction sự kiện và giới hạn backlog toàn hệ thống; không tuyên bố exactly-once cho tác động bên ngoài.

### 2026-09-05 — RUN-01b: Client tự nối lại SSE

- Shared chat client giữ client_message_id và cursor trong suốt lần gửi; lỗi mạng/EOF tự nối lại tối đa 3 lần, gửi `Last-Event-ID` và bỏ qua sự kiện đã nhận để không nối trùng delta/tool.
- Dừng ngay khi nhận done; lỗi HTTP hoặc error event từ execution được hiển thị thay vì retry tác động nghiệp vụ. Sau khi hết số lần nối lại, ID vẫn được giữ để người dùng kiểm tra lại cùng lượt.
- Unit tests giả lập stream đứt, phát lại event cũ, continuation từ cursor, HTTP conflict và ID qua retry. Frontend retry tests, TypeScript và build đều qua.
- Cursor dùng cho nối lại trong cùng lần gửi. Sau reload tab, retry dùng ID đã lưu và đọc lại từ đầu; tự resume màn hình sau reload không cần người dùng gửi lại còn là phần UX tiếp theo.

### 2026-09-05 — Triển khai lên Docker Compose server test

- Build lại backend/frontend từ commit `4ada616`, dừng hai dịch vụ trước migration và khởi động lại bằng image mới. API healthy, giao diện `http://localhost:3100` trả HTTP 200; các dịch vụ dữ liệu/RAG vẫn healthy.
- Sao lưu PostgreSQL bằng `pg_dump -Fc`, xác minh archive đọc được bằng `pg_restore --list`. Bản sao tại `.cache/deployments/20260905-chat-queue/database.dump` nằm ngoài Git; giữ image cũ với tag `before-chat-queue-20260905` cho backend/frontend.
- Migration 26 `chat_turn_identity` và 27 `durable_chat_queue` áp dụng thành công lúc 11:47:50 (UTC+7). Không sửa các migration đã áp dụng; không tự hạ schema khi đổi image.
- Smoke test qua HTTP trên backend container đang chạy, với user/workspace tạm và gateway giả lập trong mạng Docker: ngắt subscriber không dừng execution, hai câu hỏi xếp hàng FIFO, nối lại lượt đang chạy bằng `Last-Event-ID` không phát event cũ, retry lượt hoàn thành trả answer đã lưu, transcript đúng hai cặp message và chỉ có hai run succeeded. Đã dọn user/workspace/gateway tạm sau kiểm tra.
- Phạm vi xác minh triển khai là hạ tầng và contract thực thi chat. Smoke test này không đánh giá chất lượng trả lời của model thật, đăng nhập Entra hay chất lượng retrieval trên bộ KB nghiệp vụ; các giới hạn RUN-01/KB phía trên vẫn còn hiệu lực.

### 2026-09-05 — RUN-01c: Không hoàn tất stream model bị cắt

- Model Gateway chỉ coi stream thành công khi nhận DONE; EOF, JSON lỗi, upstream error và finish_reason length/filter trả lỗi an toàn. Nhận DONE kết thúc ngay, không chờ socket đóng.
- Unit tests kiểm tra stream lỗi/giới hạn/kết thúc; HTTP integration qua worker xác minh stream bị cắt không lưu assistant message hoặc phát done. Backend suite/PostgreSQL qua.

### 2026-09-05 — TOOL-01a: Schema MCP và trạng thái invocation

- Kiểm tra inputSchema gốc (required, nested types, enum, bounds) trước MCP transport, giới hạn arguments 64 KiB; không tải remote schema refs. Legacy action chưa có MCP contract giữ tương thích.
- Chat không gọi tool nếu không tạo/chuyển running được execution step. HTTP ngoài 2xx và MCP isError được ghi failed và hiện error; workflow InvokeAction cũng từ chối kết quả lỗi.
- Tests schema trước kết nối và HTTP/DB kiểm tra tool 503, lỗi tạo step; backend suite/PostgreSQL qua. Chưa hoàn tất policy action ghi, approval/idempotency/reconciliation.

### 2026-09-05 — OBS-01a: Dùng câu trả lời đã có từ tool decision

- Khi model kết thúc tool decision bằng nội dung trả lời và không yêu cầu tool, dùng nội dung đó cho answer; không gọi generation để viết lại. Trường hợp không có nội dung vẫn giữ fallback generation.
- HTTP integration qua worker/model fixture xác minh answer lưu đúng từ decision, chỉ một decision và không thêm generation chính. Còn usage toàn lượt và đưa các tác vụ phụ sang job riêng.

### 2026-09-05 — OBS-01b: Accounting từng model call

- Run có model_call step riêng cho planning, tool_decision, generation, title, suggestions; ghi model, duration, trạng thái và usage do provider trả. Không lưu prompt hoặc lỗi provider thô trong accounting; usage null khác 0.
- Unit tests kiểm tra missing/zero/failed và HTTP integration xác minh decision accounting được lưu. Còn tổng hợp toàn lượt, memory/background job, embedding/reranking accounting và hiển thị dashboard; chưa coi toàn bộ OBS-01 hoàn tất.

### 2026-09-05 — EVAL-01a: Harness qua retrieval của chat

- API kiểm thử theo workspace dùng đúng retrieveKnowledge của chat, cùng quyền/mount, fan-out, fusion và trạng thái nguồn; không gọi RAG trực tiếp theo một pipeline khác.
- CLI nhận JSONL gán nhãn, đo document-level recall/precision/MRR/nDCG, coverage KB, nguồn cấm, partial/failure và latency. Report giữ mẫu số lỗi, không lấy citation model làm đáp án chuẩn; không lưu session/câu hỏi/passage text.
- Tests kiểm tra endpoint so với retrieval trực tiếp, KB ngoài mount, nonmember, trùng document và mẫu số lỗi. Chưa có bộ câu hỏi nghiệp vụ từ người dùng để chạy baseline/candidate; chưa hoàn tất đánh giá planner/citation/model hoặc snapshot corpus.

### 2026-09-05 — KB-05a: Bảo vệ tài liệu khi ghi lại vector

- Reindex từng tài liệu kiểm tra đủ embedding, chiều vector, giá trị hữu hạn và chunk duy nhất trước khi ghi. Không xóa tài liệu trước upsert; chỉ dọn chunk cũ sau khi Qdrant xác nhận ghi thành công.
- Lỗi ghi không chạy cleanup; lỗi cleanup báo ingestion error và có thể retry với ID ổn định. Kiểm thử Qdrant local xác minh tài liệu khác được giữ nguyên, replacement ngắn hơn không còn chunk thừa và embedding sai không phá bản cũ. Toàn bộ 99 tests RAG qua trong image runtime.
- Đây chưa phải chuyển snapshot nguyên tử: trong lúc upsert/cleanup có thể thấy dữ liệu cũ và mới; lỗi ghi một phần vẫn cần generation/snapshot để rollback trọn vẹn. Reindex toàn hệ thống hiện vẫn reset collection và chưa hỗ trợ đồng thời nhiều embedding profile; KB-05 chưa hoàn tất.
- EVAL-01a bổ sung: API trả mảng rỗng cho passages/sources khi không có KB/kết quả; harness ghi nhận response sai kiểu là failed và tiếp tục các case sau. Kiểm thử HTTP/PostgreSQL cho workspace trống và 5 tests metrics/harness qua.

### 2026-09-05 — Rollout EVAL-01a / KB-05a / MCP response bound

- Backend chạy code e3bf20e; RAG chứa thay đổi f7707cc. Hai dịch vụ healthy sau recreate, không có tài liệu pending/processing tại thời điểm chuyển RAG.
- Smoke HTTP có đăng nhập trên server test qua: queue FIFO, ngắt subscriber, nối SSE theo cursor, replay answer, MCP discovery/rediscovery giữ ID, count_words và API retrieval workspace trống.
- Smoke riêng trên Qdrant v1.12.5 đang chạy xác minh replacement ngắn hơn, dọn chunk thừa, tìm dense đúng và không trả nguồn ngoài KB allowlist. Dùng collection tạm; đã xóa collection và user/workspace/gateway fixture sau kiểm tra.
- Chưa chạy baseline chất lượng nghiệp vụ vì chưa có bộ câu hỏi gán nhãn. Các hạng mục còn mở trong kế hoạch vẫn gồm embedding profiles/snapshot, reindex toàn hệ thống không reset, context budget, action ghi/approval/reconciliation, retention và accounting đầy đủ; không coi lần rollout này là hoàn tất toàn bộ kế hoạch.

### 2026-09-05 — KB-05b: Tách embedding profiles và reindex giữ chỉ mục

- Mỗi profile dùng collection riêng theo SHA-256 của workspace sở hữu, gateway endpoint và embedding model ID. API key/reranker không thuộc identity; đổi key không làm mất liên kết. Chiều vector được giữ và kiểm tra trong cấu hình Qdrant; thay deployment sau cùng model ID cần dùng model ID mới để tránh trộn không gian embedding.
- Ingest, semantic/hybrid/keyword search và inspection dùng cùng profile. Profile hoặc KB chưa có dữ liệu trả lỗi rõ ràng, không fallback sang collection legacy hay profile khác dù cùng số chiều. Xóa document/KB dọn tất cả profile trong namespace chính xác, gồm legacy, để bản cũ không xuất hiện trở lại.
- Global reindex không reset Qdrant và không xóa chunk_count cũ lúc xếp hàng. Admission dùng transaction/advisory lock; lỗi chuẩn bị rollback cả danh sách. Endpoint reset cũ trả 410. UI đã sửa mô tả thao tác.
- Tests: cùng/khác chiều, khác model/owner/gateway, key rotation, profile thiếu, đổi chiều trong profile, creation race, xóa nhiều profile; integration xác minh owner header và rollback queue khi tài liệu thứ hai lỗi, chống reindex trùng và chỉ gọi ingest.
- Rollout: mặc định KNOWLEDGE_PROFILE_READS=true. Với dữ liệu legacy, tạm đặt false để vẫn đọc collection cũ trong lúc backfill (ingest luôn ghi profile mới), kiểm tra đủ tài liệu rồi chuyển true. Không chạy bản backend cũ trong giai đoạn này vì bản cũ còn reset toàn bộ index. Legacy không tự được gán provenance từ cấu hình hiện tại.
- Giới hạn: đây là phân tách không gian embedding và giữ collection cũ, chưa phải snapshot/generation nguyên tử của cả KB. Profile mới có thể được đọc khi mới hoàn tất một phần tài liệu; chuyển model đang phục vụ cần backfill/kiểm tra trước. Job reindex vẫn chạy trong process, chưa có durable ingestion queue/lease hoặc retention tự động cho profile cũ.

### 2026-09-05 — Rollout KB-05b trên server test

- Deploy backend/RAG/frontend từ 5e4ec31. Giữ image cũ với tag before-embedding-profiles-20260905; backup PostgreSQL tại .cache/deployments/20260905-kb-profiles/database.dump đã đọc được bằng pg_restore --list.
- Tạm đọc legacy trong khi API admin reindex dựng lại đủ 3 tài liệu/67 chunk vào một profile theo cấu hình thực tế. Kiểm tra count từng tài liệu và inspection đúng, collection legacy còn nguyên; sau đó bật KNOWLEDGE_PROFILE_READS=true và recreate RAG healthy.
- Truy vấn bằng gateway thật qua API retrieval có xác thực cho cả 3 tài liệu: có bằng chứng, không partial, KB trả về đúng danh sách yêu cầu. Đây là kiểm tra kết nối và provenance, không phải baseline chất lượng trên câu hỏi nghiệp vụ gán nhãn.
- Frontend có manifest Sites rỗng, chưa gắn project_id; triển khai tiếp tục theo phạm vi server test Docker Compose đã được yêu cầu, không tạo site cloud mới.

### 2026-09-05 — CHAT-01b: Giới hạn ngữ cảnh trước mọi model call

- Model Gateway kiểm tra cả stream/generation, utility Complete và tool decision trước HTTP request. Tính kích thước JSON của messages + tool definitions; giữ system/developer instructions và toàn bộ lượt mới nhất, bỏ lượt cũ từ xa đến gần theo nhóm user/assistant/tool nguyên vẹn.
- Kiểm tra tool-call/result trước khi cắt: thiếu kết quả, kết quả mồ côi/trùng hoặc xen câu hỏi khi chưa đủ kết quả trả lỗi. Không bỏ cặp tool trong lượt hiện tại hoặc rút ngắn âm thầm câu hỏi mới/tool schema.
- Worker đọc context window từ metadata gateway với deadline 2 giây, dùng lại cho accounting. Khi có window, trừ dự phòng output tối đa 4096 (không quá 1/4 window) và framing 1024; áp trần input 128 KiB. Không có metadata vẫn áp trần byte, không bịa context window.
- Đây là heuristic bảo thủ theo byte JSON, không phải tokenizer chính xác hoặc usage tính phí. Phần dự phòng output không đặt giới hạn sinh token của provider. Ngữ cảnh bắt buộc vượt trần trả lỗi rõ cho người dùng và dừng tool round, không fallback generation bỏ qua lỗi ngân sách.
- Mỗi model_call step lưu budget: input_bytes, limit_bytes, dropped_messages, context_window, output_reserve; không lưu nội dung đã bỏ. Tests kiểm tra giữ tiếng Việt/Unicode, instruction/query/schema, chuỗi tool song song, chặn trước network và HTTP worker trim/failure/accounting. Toàn bộ backend suite/PostgreSQL qua.
- Còn phân bổ chi tiết memory/file/retrieval/tool output, tokenizer theo model, tóm tắt history có phạm vi, giới hạn output provider và UI hiển thị nội dung ngữ cảnh đã rút gọn; CHAT-01 chưa đóng toàn bộ.

### 2026-09-05 — KB-01c: Không bỏ nhầm bằng chứng và lọc quyền trước rerank

- Dedup dùng SHA-256 của toàn bộ nội dung chuẩn hóa whitespace, giữ phân biệt hoa/thường và vị trí citation (KB/document/section/page). Hai đoạn cùng 400 ký tự đầu nhưng khác điều kiện/kết luận vẫn được giữ.
- Chưa gộp các nguồn khác nhau thành một passage khi result contract chưa chứa nhiều provenance; tránh đánh mất nguồn trích dẫn do dedup xuyên tài liệu.
- Lọc từng danh sách retrieval theo đúng KB của nhánh fan-out trước fusion/reranker. Kết quả ngoài allowlist hoặc từ nhánh KB khác không được gửi sang provider rerank; vẫn giữ final ACL check.
- 111 tests RAG qua, gồm hai kết luận trái nhau sau prefix dài, citation khác trang/tài liệu/KB, mã khác hoa-thường và fixture không cho nội dung ngoài quyền tới reranker.
