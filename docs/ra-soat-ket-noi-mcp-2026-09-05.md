# Rà soát kết nối MCP — 2026-09-05

## Kết luận

Phần triển khai queue chat lên server test đã hoàn tất; điều đó không đồng nghĩa mọi hạng mục trong kế hoạch chat/agent/KB đã hoàn tất.

MCP có nền tảng giao thức phù hợp, nhưng chưa nên coi là đã chuẩn đầy đủ hoặc đủ chắc để mở rộng kết nối. Có lỗi bảo vệ credential, đồng bộ discovery và refresh token cần sửa trước. Rà soát tại commit `6207033`; lượt này không thay đổi mã chạy hoặc cấu hình server.

## Phạm vi và bằng chứng

- Đọc client MCP, egress, OAuth, lưu/publish action, API discovery và giao diện kết nối.
- Đối chiếu [đặc tả MCP 2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28) và [authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization).
- `go test ./... -count=1 -timeout 60s` qua với PostgreSQL riêng đã migrate. Test MCP hiện có bao gồm SDK server chính thức, giao thức stateless mới, lifecycle/session cũ, JSON/SSE, pagination, schema lồng nhau, structured result và `isError`.
- Ba bài tái hiện bổ sung xác nhận lỗi redirect credential, bỏ tool vì mô tả tham số dài và mất token sau refresh đồng thời. Chỉ dùng credential giả cùng database riêng; các bài này xác nhận hành vi lỗi hiện tại, không phải chứng nhận tuân thủ.
- Probe từ mạng Docker: BSR SAP MCP trả 401 kèm auth challenge; discovery resource/authorization server qua client Go của Cosmo thành công. Không dùng token người dùng để gọi nghiệp vụ SAP.
- Demo MCP: client Go khám phá đúng bốn action; `count_words` với `one two three` trả `3`. Bài này cho phép hostname demo riêng. Khi dùng đúng allowlist đang chạy, discovery bị `ErrPrivateAddress` trước khi mở kết nối.
- Mã tái hiện cục bộ được giữ ở `.cache/mcp-review/review_audit_test.go` ngoài Git; file test tạm trong package đã được dọn. Không sửa hoặc rediscover danh sách action đang lưu của người dùng.

## Các phát hiện cần sửa

### MCP-REV-01 — P1: Redirect có thể đưa credential sang origin khác

**Vị trí:** `backend/internal/tools/mcp.go:35`, `backend/internal/tools/invoke.go:142`.

`mcpAuthorisingTransport.RoundTrip` gắn lại credential của tool vào mỗi HTTP request. `CheckRedirect` chỉ giới hạn số lần chuyển hướng, không kiểm tra origin. Vì vậy, dù HTTP client gỡ Authorization khi chuyển hostname, wrapper lại gắn token trước khi gửi request tiếp theo.

**Tái hiện:** server fixture tại `127.0.0.1` trả 307 sang hostname `localhost`; đích mới nhận bearer token giả của tool gốc. Không sử dụng hay ghi log credential thật. Endpoint có redirect sai cấu hình hoặc bị chiếm quyền có thể làm lộ credential; đây không phải bằng chứng credential hiện tại đã bị lộ.

**Đề xuất:** mặc định từ chối redirect khác origin và HTTPS xuống HTTP; kiểm tra origin trong transport trước khi gắn credential. Nếu cần chuyển endpoint, cấu hình lại rõ ràng. Rà soát cùng chính sách với custom header và token endpoint.

**Điều kiện hoàn tất:** kiểm tra redirect cùng origin, khác hostname/port/scheme và downgrade; đích không được phép không nhận credential hoặc request có dữ liệu nhạy cảm.

### MCP-REV-02 — P1: Discovery thay danh sách action không nguyên tử

**Vị trí:** `backend/internal/httpapi/tools.go:416–428`.

API xóa từng action hiện tại trước rồi lưu từng action mới. Lỗi xóa bị bỏ qua; lỗi lưu chỉ `continue`; cuối cùng vẫn trả 200. Khi một lần lưu lỗi hoặc request bị hủy giữa chừng, draft có thể mất action hoặc chỉ còn một phần. Hai discovery/publish đồng thời cũng không có một khóa/transaction bao trùm để bảo đảm thấy một danh sách đầy đủ.

Đây là kết luận từ đường đi mã nguồn, chưa tiêm lỗi database vào endpoint đang triển khai. Snapshot phiên bản đã publish không bị vòng xóa draft này trực tiếp sửa; rủi ro nằm ở draft/test và lần publish cạnh tranh hoặc tiếp theo.

**Đề xuất:** tải và validate toàn bộ discovery trước; thay danh sách trong một transaction có khóa tool, rollback khi bất kỳ thao tác nào lỗi. Dùng cùng khóa với publish. Chỉ báo thành công sau commit; cân nhắc giữ action ID ổn định theo remote name.

**Điều kiện hoàn tất:** lỗi tại action thứ N không làm đổi danh sách cũ; discovery cạnh tranh với discovery/publish không tạo snapshot thiếu hoặc trộn dữ liệu.

### MCP-REV-03 — P2: Tool hợp lệ bị bỏ qua mà không báo thiếu

**Vị trí:** `backend/internal/tools/mcp.go:128–150`, `backend/internal/tools/mcp.go:201`.

Schema gốc được giữ cho model, nhưng discovery vẫn phụ thuộc projection của editor: `mcpParameters` gọi `CleanParameters`, vốn từ chối mô tả tham số dài hơn 512 ký tự. Discovery bỏ cả tool khi projection lỗi. Ngoài ra, chạm 100 action hoặc hết 20 trang cũng trả success mà không có cờ incomplete; cursor lặp không được phân biệt với danh sách đã đọc hết.

**Tái hiện:** server dùng SDK chính thức công bố tool với inputSchema hợp lệ và mô tả một property dài 513 ký tự. Cosmo trả discovery thành công với 0 action. Khi kết hợp MCP-REV-02, một lần discovery như vậy có thể xóa draft đang có.

**Đề xuất:** projection chỉ phục vụ hiển thị, không được quyết định tool hợp lệ hay không; giới hạn phần hiển thị nhưng giữ schema nguyên bản. Trả rõ lý do tool bị từ chối, giới hạn số lượng/trang và cursor lặp. Không thay registry bằng kết quả chưa đầy đủ.

### MCP-REV-04 — P2: Refresh đồng thời có thể xóa token vừa gia hạn

**Vị trí:** `backend/internal/tools/oauth_user.go:574–626`.

Mỗi request tự đọc token và refresh, không có cơ chế phối hợp theo `(tool_id, user_id)`. Provider xoay refresh token có thể chấp nhận request đầu và từ chối request thứ hai. Nhánh lỗi xóa token theo tool/user, không kiểm tra phiên bản vừa đọc, nên có thể xóa token mới mà request đầu đã lưu.

**Tái hiện:** hai request cùng dùng refresh token cũ; request đầu nhận và lưu token mới; request sau nhận `invalid_grant` rồi xóa bản ghi mới. Kết quả: một request refresh thành công nhưng kết nối người dùng vẫn biến mất.

Nhánh này cũng xóa token khi provider lỗi tạm thời hoặc context bị hủy, khiến người dùng phải kết nối lại dù refresh token có thể vẫn còn hiệu lực.

**Đề xuất:** phối hợp refresh theo tool/user, có hiệu lực giữa nhiều worker/replica; cập nhật bằng version hoặc compare-and-swap. Phân biệt lỗi token vĩnh viễn với timeout/5xx/cancellation. Chỉ xóa đúng phiên bản token đã bị xác nhận vô hiệu.

### MCP-REV-05 — P2: OAuth còn lệch contract về challenge và issuer

**Vị trí:** `backend/internal/tools/oauth_user.go:244–245`, `:392–395`, `:472–473`; `backend/internal/tools/mcp.go:46–48`.

Discovery chỉ lấy `resource_metadata` từ challenge, không giữ `scope`. Khi bắt đầu OAuth, code lấy scope cấu hình hoặc metadata. Trong khi gọi MCP, 401/403 bị gộp thành `ErrToolUnauthorized` và bỏ challenge, nên không có thông tin để xử lý yêu cầu tăng quyền. `equalIssuer` còn bỏ dấu `/` cuối trước khi so sánh.

Đặc tả yêu cầu coi scope trong challenge là nguồn quyết định cho thao tác hiện tại và so sánh issuer phản hồi đúng chuỗi, không chuẩn hóa dấu `/`. Đây là khoảng trống đối chiếu mã với [MCP authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization), chưa kiểm thử end-to-end với provider yêu cầu tăng quyền.

**Đề xuất:** giữ challenge có cấu trúc; hướng dẫn kết nối/tăng quyền phù hợp và không tự chạy lại tool có tác động chưa xác định. So sánh issuer chính xác tại callback. Ghi rõ adapter Entra là profile tương thích, không dùng nó để tuyên bố mọi provider đều tuân thủ cùng contract.

### MCP-REV-06 — P2: Demo đã đăng ký nhưng bị cấu hình egress chặn

**Cấu hình đang chạy:** backend chỉ có `TOOL_EGRESS_ALLOWED_HOSTS=host.docker.internal`; Demo MCP đã lưu endpoint `http://mcpdemo:8090/mcp`.

Hostname demo trỏ vào mạng private nhưng không nằm trong allowlist. Đã tái hiện bằng client Go trong cùng mạng Docker và đúng allowlist của backend: `ErrPrivateAddress`.

**Đề xuất:** thêm đúng `mcpdemo` nếu muốn dùng demo trên server test; giữ các hostname đã cho phép. Recreate backend rồi kiểm tra discovery/try-it qua API. Không mở wildcard hay toàn bộ dải private.

## Những phần đã làm tốt

- Dùng `github.com/modelcontextprotocol/go-sdk v1.7.0`, thay vì tự cài một phần JSON-RPC/lifecycle.
- Có version negotiation, Streamable HTTP, session của server cũ và đóng session sau thao tác; không tự retry invocation sau lỗi mạng.
- Giữ nested inputSchema, outputSchema, annotations và metadata chuẩn được SDK nhận diện qua lưu/publish; alias gọi model tách với remote name.
- Có timeout, giới hạn nội dung đưa vào model, ánh xạ `isError`, mã hóa token và phân tách token theo user/tool.
- OAuth có PKCE S256, state một lần gắn người dùng, discovery metadata và kiểm tra issuer callback khi có trường `iss`.

Các điểm trên không thay thế việc sửa các trường hợp lỗi đã nêu. Việc cắt result sau khi SDK decode cũng không chứng minh đã giới hạn bộ nhớ khi đọc response từ mạng; nên bổ sung kiểm tra payload lớn ở transport.

## Thứ tự thực hiện đề nghị

1. MCP-REV-01: khóa đích nhận credential.
2. MCP-REV-02 + MCP-REV-03: discovery đầy đủ và thay registry nguyên tử.
3. MCP-REV-04: refresh an toàn khi nhiều request cùng chạy.
4. MCP-REV-05: hoàn thiện challenge/issuer và kiểm thử provider.
5. MCP-REV-06: sửa cấu hình demo, kiểm tra lại qua API trên server test.

Mỗi phần cần regression test phù hợp, commit riêng và kiểm tra lại sau triển khai. Kiểm tra đăng nhập Entra thực, gọi SAP bằng quyền người dùng, phản hồi quá lớn và tool có tác động ghi vẫn là các phần chưa được xác minh trong lượt rà soát này.

## Tiến độ khắc phục

- MCP-REV-01: Chặn redirect khác origin trước khi gửi request; MCP kiểm tra origin trước khi đọc/gắn credential. Áp dụng cùng HTTP client cho custom header và OAuth token exchange. Tests kiểm tra 302/307/308, khác hostname/port, downgrade và guard trước đọc secret; tools suite qua.

- MCP-REV-02/03: Discovery từ chối danh sách thiếu, cursor lặp, quá giới hạn hoặc contract lỗi; projection cắt mô tả hiển thị nhưng giữ schema gốc. Thay registry trong transaction, giữ ID theo remote name và kiểm tra revision; Save/Delete action và Publish dùng cùng khóa tool. Tests PostgreSQL kiểm tra rollback khi insert thứ hai lỗi, revision cũ, publish cạnh tranh; toàn bộ backend suite qua.

- MCP-REV-04: Token gần hết hạn được đọc lại dưới row lock PostgreSQL trước refresh; sáu caller qua các repository riêng chỉ gọi provider một lần. Chỉ invalid_grant xóa token đang khóa; 5xx/cancellation giữ kết nối. Tests rotation, lỗi tạm thời, invalid_grant và hủy đều qua với database riêng.

- MCP-REV-05: Giữ scope từ challenge, đưa vào yêu cầu cấp quyền; lỗi 401/403 giữ loại lỗi/status/scope có giới hạn và không đưa error_description upstream ra UI. Callback so issuer đúng chuỗi, kể cả provider error; tests dấu slash/case/thiếu issuer và scope challenge qua. Tăng quyền hiện dùng cấu hình Scope và Connect hiện có, chưa có màn hình consent riêng tự động theo từng action; không tự retry invocation.
