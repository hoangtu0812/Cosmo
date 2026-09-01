# Kế hoạch triển khai Agent, Tool, Skill, Workflow và Library cho Cosmo

> **Phiên bản:** 1.0  
> **Ngày lập:** 2026-09-01  
> **Baseline:** `ebe8491 feat: make knowledge bases workspace-scoped`  
> **Tài liệu liên quan:** [Kiến trúc Enterprise AI Platform](enterprise_ai_platform_architecture.md), [Kế hoạch triển khai tổng thể](enterprise_ai_platform_implementation_plan.md)  
> **Mục đích:** Chuyển lộ trình sản phẩm sau Knowledge Base thành kế hoạch delivery có thể phân rã backlog, ước lượng, giao việc và nghiệm thu.

> **Trạng thái triển khai:** P0 foundation đã hoàn thành ngày 2026-09-01. Cosmo hiện có migration version/checksum/advisory lock, Run/Step/Event state machine, durable PostgreSQL worker queue, API quan sát/hủy run và chat là execution path đầu tiên phát sinh run. Các mục hardening còn lại được ghi tại phần 5.3 và phải hoàn tất trước khi mở Workflow Designer cho production.

---

## 1. Tóm tắt điều hành

Knowledge Base đã hoàn thành lát cắt đầu tiên theo hướng workspace-scoped: mỗi KB dùng Model Gateway của workspace, có cấu hình retrieval/chunking riêng, trạng thái xử lý và giao diện quản trị mới.

Phần tiếp theo của Cosmo nên được triển khai theo thứ tự phụ thuộc sau:

```text
Foundation
├── Agent lifecycle
├── Tool registry
└── Skill registry
      ↓
Workflow designer + Run Engine
      ↓
Schedule + Project + Artifact
      ↓
Observability + Evaluation
      ↓
Library + Distribution
```

Không nên bắt đầu bằng Workflow Designer. Designer chỉ là lớp trình bày; nếu chưa có version model, Run/Step/Event, worker, retry, credential và capability contract thì phần execution phía sau sẽ phải viết lại.

### 1.1. Thời lượng tham khảo

Giả định:

- Một sprint kéo dài 2 tuần.
- Một squad gồm 3–4 developer, một QA và UX/Product bán thời gian.
- Giữ Go backend dạng modular monolith trong giai đoạn này.
- Worker có thể deploy riêng nhưng dùng chung codebase và PostgreSQL.

| Giai đoạn | Sprint | Thời lượng | Kết quả chính |
|---|---:|---:|---|
| 0. Củng cố nền | 1–2 | 4 tuần | Migration versioned, domain boundaries, worker, Run/Step/Event |
| 1. Chuẩn hóa Agent | 3–4 | 4 tuần | Draft/publish/version/clone, test console, capability và quyền |
| 2. Tool và Skill | 5–6 | 4 tuần | HTTP/OpenAPI, MCP, credential vault, Skill versioning |
| 3. Workflow MVP | 7–9 | 6 tuần | Designer, execution engine, condition, approval, retry và timeout |
| 4. Schedule và Project | 10–11 | 4 tuần | Manual/cron/webhook, Project, run history và artifact |
| 5. Observability | 12–13 | 4 tuần | Trace, token/cost/latency, budget, alert và evaluation |
| 6. Library | 14–15 | 4 tuần | Publish/share/install/update Agent, Workflow, Skill và Knowledge |

**Tổng thời gian tham khảo:** 30 tuần. Các workstream UX, QA, security và observability instrumentation được thực hiện xuyên suốt, không chờ tới phase cuối.

---

## 2. Hiện trạng và khoảng trống

### 2.1. Năng lực đã có

- Microsoft Entra và local authentication.
- Workspace, membership, invitation và workspace-scoped Model Gateway.
- Chat, conversation và message history.
- Knowledge Base:
  - upload, ingestion, chunking và retrieval;
  - semantic/keyword/hybrid search;
  - embedding, reranker và chunking theo KB;
  - publish/share/install/update;
  - citation, document inspection và ingestion event;
  - API key được mã hóa ở workspace.
- Agent cơ bản:
  - CRUD;
  - system prompt và model;
  - Knowledge Base bindings;
  - opening line, preset questions và memory;
  - private/workspace visibility.
- Audit log cơ bản.

### 2.2. Khoảng trống cần xử lý

- Database migration hiện được chạy từ danh sách SQL lúc backend khởi động, chưa có version/checksum rõ ràng.
- Agent chưa có draft, published release, immutable version hoặc clone.
- Chưa có Tool, Skill, Workflow, Project và Library domain.
- Chưa có Run Engine chung; chat và ingestion vẫn có vòng đời riêng.
- Chưa có worker queue/outbox chung, retry policy và cancellation contract.
- Chưa có credential registry dùng chung cho Tool/MCP.
- Chưa có trace tree Run → Step → Model/Retrieval/Tool.
- Chưa có budget và alert theo workspace/project/agent.
- Chưa có compatibility/dependency model để update artifact an toàn.

---

## 3. Nguyên tắc kiến trúc

1. **Workspace là security boundary chính.** Mọi resource, run, credential và artifact phải có workspace owner rõ ràng.
2. **Published version là bất biến.** Runtime không được đọc trực tiếp một draft đang thay đổi.
3. **Execution phải durable.** Restart backend hoặc worker không được làm mất run hoặc approval.
4. **Event là append-only.** Trạng thái hiện tại có thể cập nhật, nhưng lịch sử step/event không được viết lại.
5. **Credential không đi qua prompt.** Runtime chỉ resolve secret ngay trước outbound request.
6. **Tool output là dữ liệu không tin cậy.** Phải giới hạn kích thước, validate và không xem output là instruction hệ thống.
7. **Mọi side effect phải idempotent.** Retry không được tạo tác động nghiệp vụ trùng lặp.
8. **Observability là acceptance criterion.** Mỗi capability mới phải phát metric/event từ lần đầu phát hành.
9. **Library phân phối release, không phân phối draft.** Installation phải pin version cụ thể.
10. **Không tách microservice sớm.** Chỉ tách khi có nhu cầu scale, trust boundary hoặc ownership độc lập.

---

## 4. Kiến trúc đích của lát cắt này

### 4.1. Backend modules

```text
backend/internal/
├── agents/          Agent definition, version và publication
├── tools/           Tool definition, adapter và execution policy
├── skills/          Skill definition, version và dependency
├── workflows/       Graph, validation và publication
├── runs/            Run/Step/Event state machine
├── workers/         Job claim, heartbeat, retry và recovery
├── projects/        Project, environment và artifact
├── credentials/     Encrypted workspace credentials
├── library/         Release, installation và update
├── observability/   Usage, trace, cost, budget và alert
└── httpapi/         Authentication, authorization và transport
```

### 4.2. Core data model

```text
Definition ──1:N── Version ──1:N── Release
                                │
                                └── Installation

Run ──1:N── Step ──1:N── Event
 │         │
 │         └── Artifact
 └── Trigger / Project / Workspace / Actor
```

### 4.3. Runtime flow

```text
API/Trigger
→ authorize
→ resolve immutable release snapshot
→ create run + outbox job
→ worker claims job
→ execute step
→ append events
→ persist output/artifact
→ enqueue next step hoặc wait approval
→ finalize run
```

---

## 5. Giai đoạn 0 — Củng cố nền

**Thời lượng:** Sprint 1–2  
**Mục tiêu:** Tạo nền tảng dữ liệu và runtime dùng chung trước khi mở rộng Agent/Tool/Workflow.

**Trạng thái:** Foundation hoàn thành ngày 2026-09-01.

| Hạng mục | Trạng thái | Hiện thực |
|---|---|---|
| Migration versioned | Hoàn thành | `schema_migrations`, checksum bất biến, advisory lock, transaction, `cosmo-migrate up/status` |
| Run/Step/Event | Hoàn thành | `internal/runs`, state machine, sequence tăng đơn điệu, idempotency key |
| Durable worker | Hoàn thành nền tảng | PostgreSQL queue, `SKIP LOCKED`, lease, heartbeat, retry/backoff, permanent error và recovery |
| Run API | Hoàn thành | List/detail/steps/events/SSE/cancel có workspace authorization |
| Integration đầu tiên | Hoàn thành | Chat ghi retrieval/model step và kết thúc run theo kết quả thực tế |
| Domain boundary | Hoàn thành nền tảng | Migration và execution tách khỏi HTTP; các domain mới phải phụ thuộc qua package contract |

### 5.1. Sprint 1 — Migration và domain boundaries

#### Database

- Tạo migration runner có:
  - version tăng tuần tự;
  - checksum;
  - trạng thái applied/failed;
  - lệnh `status` và `up`;
  - lock để hai replica không migrate đồng thời.
- Chuyển migration hiện tại sang file versioned mà không làm mất database đang chạy.
- Thêm kiểm thử:
  - database rỗng;
  - database hiện tại;
  - chạy migration hai lần;
  - migration lỗi giữa chừng.
- Không chạy schema mutation ngoài migration framework sau khi chuyển đổi.

#### Domain

- Tách business logic khỏi `internal/httpapi` theo module.
- Chuẩn hóa repository/service interface.
- Chuẩn hóa error codes, pagination và API envelope.
- Tạo policy helper dùng chung cho:
  - workspace membership;
  - view/edit/test/publish/delete;
  - resource ownership.
- Viết ADR cho:
  - modular monolith;
  - versioned artifact;
  - run state machine;
  - credential ownership;
  - artifact storage.

#### Đầu ra

- `cmd/migrate` hoặc subcommand migration tương đương.
- Module skeleton và dependency rules.
- Migration runbook.
- ADR đã review.

#### Tiêu chí nghiệm thu

- Database hiện tại nâng cấp thành công và dữ liệu giữ nguyên.
- Backend có thể khởi động trên schema đúng version.
- Deploy từ hai replica không chạy trùng migration.
- Domain service có test độc lập với HTTP.

### 5.2. Sprint 2 — Run/Step/Event và worker

#### Data model

- `runs`:
  - workspace/project/actor;
  - trigger type;
  - resource type/id/version;
  - status;
  - input/output summary;
  - started/finished/cancelled timestamps;
  - idempotency key;
  - trace ID.
- `run_steps`:
  - node/type/name;
  - attempt;
  - status;
  - input/output references;
  - timeout;
  - error code/message.
- `run_events`:
  - sequence;
  - event type;
  - payload đã redaction;
  - created time.
- `worker_jobs` hoặc transactional outbox:
  - available time;
  - lease owner/expiry;
  - attempt/max attempts;
  - dedupe key.

#### State machine

```text
queued → running → succeeded
                 ↘ failed
                 ↘ cancelled
                 ↘ timed_out
                 ↘ waiting_approval → running
```

#### Worker behavior

- Claim bằng database lease.
- Heartbeat và lease recovery.
- Exponential backoff có jitter.
- Phân biệt transient và permanent error.
- Cancellation cooperative.
- Timeout cấp run và step.
- SSE stream theo run event sequence.
- Payload lớn lưu ở object storage, event chỉ giữ reference.

#### Tiêu chí nghiệm thu

- Restart worker không làm mất run.
- Retry không tạo hai run cho cùng idempotency key.
- Event sequence tăng đơn điệu và có thể resume stream.
- Có thể cancel run đang queued/running.
- Không có API key trong job, run, event hoặc log.

### 5.3. Hardening trước Workflow production

Các điểm dưới đây không chặn P1 Agent lifecycle, nhưng là gate bắt buộc trước khi workflow có side effect thực tế:

- Thêm integration test tự động cho database rỗng, database nâng cấp và hai replica migrate đồng thời.
- Thêm integration test worker restart/lease recovery và idempotency dưới tải đồng thời.
- Thêm cancellation polling cho handler chạy dài và timeout policy ở cấp run.
- Thêm jitter vào retry backoff và dead-letter/replay tooling cho job thất bại vĩnh viễn.
- Tách tiếp Agent repository/service khỏi `internal/httpapi` trong P1; không mở rộng business logic Agent trực tiếp trong handler.
- Chuẩn hóa policy helper và API error catalog khi thêm quyền draft/test/publish ở P1.
- Lưu payload/output lớn ở object storage; Run/Event chỉ giữ metadata và `output_ref`.

---

## 6. Giai đoạn 1 — Chuẩn hóa Agent

**Thời lượng:** Sprint 3–4  
**Mục tiêu:** Biến Agent từ cấu hình chat có thể sửa trực tiếp thành artifact có vòng đời rõ ràng.

### 6.1. Sprint 3 — Agent lifecycle

#### Data model

- `agent_definitions`: identity ổn định, owner, visibility và metadata.
- `agent_versions`: snapshot bất biến của:
  - model;
  - system prompt;
  - opening line;
  - preset questions;
  - memory policy;
  - KB bindings;
  - Tool/Skill bindings;
  - runtime limits.
- `agent_drafts`: draft hiện tại và revision để optimistic locking.
- `agent_releases`: published version, changelog và người publish.

#### API

- Create draft.
- Update draft theo revision.
- Validate draft.
- Publish.
- Clone.
- Archive/deprecate.
- Compare versions.
- Restore một published version thành draft mới.

#### Migration dữ liệu cũ

- Mỗi Agent hiện tại trở thành definition.
- Cấu hình hiện tại được snapshot thành version `1`.
- Không thay đổi conversation cũ.
- Conversation mới lưu `agent_version_id`.

#### Tiêu chí nghiệm thu

- Sửa draft không ảnh hưởng published version.
- Publish tạo snapshot bất biến.
- Hai editor không âm thầm ghi đè nhau.
- Clone không dùng chung mutable configuration.

### 6.2. Sprint 4 — Test Console, capability và quyền

#### Test Console

- Chạy Agent draft qua Run Engine.
- Hiển thị:
  - prompt/model;
  - retrieval passages;
  - Tool/Skill calls;
  - token, latency và error;
  - final answer và citation.
- Lưu test run riêng với production run.
- Cho phép replay cùng input.
- So sánh draft với published version.

#### Model capability

- Chuẩn hóa capability manifest:
  - chat;
  - vision;
  - tool calling;
  - structured output;
  - streaming;
  - context/output limit.
- Validate Agent trước publish.
- Không hiển thị model không phù hợp với capability Agent yêu cầu.

#### Permission

- `view_agent`
- `edit_agent`
- `test_agent`
- `publish_agent`
- `share_agent`
- `delete_agent`

#### Tiêu chí nghiệm thu

- User có thể draft → test → publish → chat bằng release.
- Production chat luôn truy ra đúng Agent version.
- User chỉ có quyền view không thể test hoặc publish.
- Capability mismatch bị chặn trước runtime.

---

## 7. Giai đoạn 2 — Tool và Skill

**Thời lượng:** Sprint 5–6  
**Mục tiêu:** Tạo capability có schema, version và credential an toàn để Agent/Workflow sử dụng.

### 7.1. Sprint 5 — Tool Registry và Credential Vault

#### Credential Vault

- Credential thuộc workspace và environment.
- Hỗ trợ API key, bearer token, basic auth và OAuth reference.
- Secret mã hóa bằng secret service hiện có.
- API GET chỉ trả metadata/masked value.
- Có rotate, disable, test connection và audit.
- Runtime resolve credential JIT ngay trước outbound call.

#### HTTP/OpenAPI Tool

- Import OpenAPI hoặc tạo operation thủ công.
- Input/output JSON Schema.
- Base URL và credential reference.
- Allowed domains và HTTPS policy.
- Header/query/body mapping.
- Timeout, retry, response size limit.
- Redaction paths.
- Test console với sample input.

#### MCP Tool

- Workspace-managed remote MCP trước.
- Discover tools/resources/prompts.
- Snapshot capability khi publish.
- Detect schema drift.
- Egress allowlist, SSRF và DNS rebinding protection.
- Disable/kill switch theo server/tool/workspace.

#### Tiêu chí nghiệm thu

- Agent không nhìn thấy plaintext credential.
- Tool chỉ gọi domain được allowlist.
- Schema drift chuyển installation sang `review_required`.
- Timeout/retry được ghi thành step events.
- Response quá giới hạn bị chặn trước khi đưa vào model context.

### 7.2. Sprint 6 — Skill Registry

#### Skill model

- Name, introduction, tags và owner.
- Instructions.
- Input/output contract.
- Tool dependencies.
- Model capability requirements.
- Tài nguyên/asset đính kèm.
- Examples và test cases.
- Draft/published/archive lifecycle.

#### Skill execution

- Skill được resolve thành immutable version lúc bắt đầu run.
- Validate Tool dependencies trước publish/install.
- Giới hạn kích thước instructions/assets.
- Không cho skill tự mở rộng permission của Tool.
- Test Skill độc lập và test khi gắn vào Agent.

#### Tiêu chí nghiệm thu

- Có thể tạo, test, publish và gắn Skill vào Agent draft.
- Published Agent pin chính xác Skill/Tool version.
- Update Skill không tự thay đổi Agent đang chạy.
- Missing dependency được phát hiện trước publish.

---

## 8. Giai đoạn 3 — Workflow MVP

**Thời lượng:** Sprint 7–9  
**Mục tiêu:** Cho phép thiết kế và thực thi workflow có kiểm soát trên Run Engine.

### 8.1. Sprint 7 — Graph model và designer

#### Graph schema

- Workflow definition, draft, version và release.
- Node chứa `id`, `type`, `position`, `config` và schema version.
- Edge chứa source, target, handle và optional condition.
- Workflow-level input/output JSON Schema.
- Dependency manifest pin Agent/Tool/Skill release.

#### Validation

- Có đúng một Start node.
- Không có edge trỏ tới node không tồn tại.
- Không có node mồ côi trừ node được đánh dấu disabled.
- Condition phải có nhánh hợp lệ.
- Mapping phải đúng input schema của node đích.
- Phát hiện cycle; chỉ cho phép loop qua node kiểm soát chuyên biệt ở phase sau.
- Dependency phải published và người dùng có quyền.

#### Designer UI

- Canvas, zoom, pan và snap grid.
- Node palette.
- Node inspector bên phải.
- Connect/disconnect edges.
- Autosave draft.
- Undo/redo.
- Validation panel và focus tới node lỗi.
- Keyboard accessibility và reduced motion.

#### Tiêu chí nghiệm thu

- Có thể dựng và lưu graph Start → Agent → Output.
- Invalid graph không được publish.
- Reload không làm mất vị trí node/draft.
- Hai editor được bảo vệ bằng revision conflict.

### 8.2. Sprint 8 — Execution engine

#### Node MVP

- Start/Input.
- Agent.
- Tool.
- Condition.
- Output.
- Delay ngắn.

#### Runtime

- Snapshot workflow và toàn bộ dependencies khi tạo run.
- Mỗi node tương ứng một hoặc nhiều run step.
- Input/output mapping an toàn, không dùng arbitrary code evaluation.
- Persist checkpoint sau mỗi step.
- Retry step theo policy.
- Timeout step/run.
- Cancel và resume.
- Step output lớn lưu thành artifact.
- SSE cập nhật timeline trực tiếp.

#### Tiêu chí nghiệm thu

- Workflow Agent → Tool → Condition → Output chạy end-to-end.
- Restart worker giữa hai node vẫn tiếp tục đúng node tiếp theo.
- Retry không thực thi lại step đã thành công.
- Run lưu chính xác dependency versions.

### 8.3. Sprint 9 — Approval và Run Inspector

#### Approval node

- Approver user/group/role.
- Approval reason và exact payload preview.
- Approve/reject.
- Expiry và timeout action.
- Re-authorization tại thời điểm execution.
- Payload sau approval không được thay đổi.
- Separation of duties tùy chọn.

#### Run Inspector

- Timeline Run → Step → Event.
- Input/output đã redaction.
- Model/tool/retrieval metadata.
- Retry một step hợp lệ.
- Replay từ đầu với cùng input.
- Download artifact.
- Copy trace ID.

#### Tiêu chí nghiệm thu

- Approval pending sống qua restart/deploy.
- Reject kết thúc đúng nhánh và có audit.
- Người không nằm trong approver set không thể duyệt.
- Mọi step có duration, attempt, input/output reference và error rõ ràng.

---

## 9. Giai đoạn 4 — Schedule và Project

**Thời lượng:** Sprint 10–11  
**Mục tiêu:** Đưa workflow từ test thủ công thành ứng dụng có trigger, lịch sử và artifact.

### 9.1. Sprint 10 — Trigger

#### Manual

- Form sinh từ workflow input schema.
- Validate phía frontend và backend.
- Idempotency cho submit lặp.

#### Cron

- Cron expression và timezone.
- Enable/disable.
- Next run preview.
- Misfire policy: skip, run once hoặc catch up.
- Concurrency policy: allow, forbid hoặc replace.
- Distributed scheduler lock.

#### Webhook

- Secret/signature riêng theo trigger.
- Timestamp và replay protection.
- Payload size/content-type limit.
- Schema validation.
- Rotate/revoke secret.
- Rate limit và audit.

#### Tiêu chí nghiệm thu

- Một workflow có thể chạy bằng cả manual, cron và webhook.
- Hai scheduler replica không tạo run trùng.
- Webhook replay không tạo business effect lần hai.
- Disabled schedule không tạo run mới.

### 9.2. Sprint 11 — Project và Artifact

#### Project

- Project metadata, owner, members và environments.
- Gắn Agent, Workflow và Schedule.
- Environment-specific credential references.
- Run history và status summary.
- Budget placeholder để Observability sử dụng.

#### Artifact

- Metadata trong PostgreSQL.
- Nội dung lớn trong MinIO.
- Hỗ trợ file, JSON, report và generated output.
- Link tới run/step tạo artifact.
- MIME, size, checksum và retention.
- Authenticated download và audit.
- Cleanup job theo retention.

#### Tiêu chí nghiệm thu

- Project hiển thị workflow, schedule, run và artifact liên quan.
- User không tải artifact ngoài workspace/project quyền hạn.
- Xóa/expire artifact không để metadata sai lệch.
- Có lineage từ artifact về exact run/step/version.

---

## 10. Giai đoạn 5 — Observability

**Thời lượng:** Sprint 12–13  
**Mục tiêu:** Biến dữ liệu runtime thành khả năng vận hành, tối ưu và kiểm soát chi phí.

### 10.1. Sprint 12 — Usage, trace và dashboard

#### Telemetry bắt buộc

- Queue duration.
- Run/step duration.
- Model provider/model/version.
- Input/output/cached tokens.
- Estimated cost.
- Time to first token.
- Retrieval candidate/result count và score.
- Tool latency/status/error.
- Retry, timeout và cancellation.
- Artifact count/size.

#### Trace model

```text
Run
├── Agent Step
│   ├── Retrieval
│   ├── Model Call
│   └── Tool Call
└── Output Step
```

#### Dashboard

- Overview theo workspace/project.
- Success/error/timeout rate.
- Token/cost/latency trend.
- Top Agent/Workflow/Tool.
- Error grouping.
- Drill-down tới run/step.
- Filter theo thời gian, user, model, status và resource.

#### Tiêu chí nghiệm thu

- Từ một dashboard error có thể mở exact failed step.
- Token/cost tổng hợp khớp dữ liệu step trong sai số cho phép.
- Trace không chứa plaintext secret hoặc dữ liệu đã cấu hình redaction.
- Metrics có workspace/project/resource dimensions.

### 10.2. Sprint 13 — Budget, alert và evaluation

#### Budget

- Budget theo workspace/project/Agent/Workflow.
- Daily/monthly window.
- Alert ở 50/75/90/100%.
- Soft limit và hard limit.
- Circuit breaker theo model/tool.
- Budget override có expiry và audit.

#### Alert

- Error rate.
- Queue backlog.
- Latency p95.
- Tool/provider outage.
- Schedule missed.
- Cost spike.
- Retrieval quality regression.

#### Evaluation

- Golden test set cho Agent/Workflow.
- Expected Tool selection/arguments.
- Expected/forbidden source.
- Response rubric và human rating.
- So sánh release candidate với current release.
- Publish gate dựa trên regression threshold.

#### Tiêu chí nghiệm thu

- Hard budget chặn run mới nhưng không làm hỏng run đang finalize.
- Alert chỉ tạo một incident trong dedupe window.
- Release không được publish nếu evaluation gate thất bại.
- Evaluation report lưu exact dependency versions.

---

## 11. Giai đoạn 6 — Library

**Thời lượng:** Sprint 14–15  
**Mục tiêu:** Chuẩn hóa việc phân phối, cài đặt và cập nhật artifact giữa các workspace.

### 11.1. Sprint 14 — Unified Registry

#### Artifact types

- Agent.
- Workflow.
- Skill.
- Knowledge Base.

#### Release metadata

- Artifact identity và type.
- Owner workspace/user.
- Semantic version hoặc monotonic release version.
- Changelog.
- Tags và introduction.
- Visibility.
- Dependency manifest.
- Capability/permission manifest.
- Compatibility requirements.
- Checksum/provenance.
- Deprecated/suspended state.

#### Governance

- Draft → submitted → approved → published.
- Review khi permission mở rộng.
- Kill switch/suspend.
- Audit publish/deprecate/suspend.
- Published release bất biến.

#### Tiêu chí nghiệm thu

- Bốn artifact types dùng chung release/install contract.
- Dependency và permission hiển thị trước publish.
- Suspended release không thể được cài mới.
- Runtime có thể ngừng capability bị kill switch.

### 11.2. Sprint 15 — Install, update và fork

#### Library UI

- Browse/search/filter/tags.
- Detail và changelog.
- Dependency/permission preview.
- Install vào workspace/project.
- Installed version và update available.
- Diff version.
- Update, uninstall và fork.

#### Update safety

- Installation pin exact release.
- Không auto-update breaking change.
- Permission expansion yêu cầu re-consent.
- Validate dependency trước update.
- Rollback về installed version trước.
- Uninstall kiểm tra resource đang phụ thuộc.

#### Tiêu chí nghiệm thu

- Workspace có thể publish → share → install → update một artifact.
- Runtime không đổi hành vi cho tới khi workspace xác nhận update.
- Fork tạo definition riêng, không còn mutable dependency vào nguồn.
- Uninstall/suspend revoke runtime capability trong SLA xác định.

---

## 12. Ma trận phụ thuộc

| Capability | Phụ thuộc bắt buộc |
|---|---|
| Agent versioning | Migration framework, workspace policy |
| Agent Test Console | Run/Step/Event, model/retrieval instrumentation |
| HTTP/MCP Tool | Credential Vault, Tool schema, worker timeout |
| Skill | Tool release và dependency manifest |
| Workflow Designer | Agent/Tool release, graph schema |
| Workflow Runtime | Run Engine, worker, immutable dependency snapshot |
| Approval | Durable run state, authorization, audit |
| Cron/Webhook | Idempotency, scheduler lock, Run Engine |
| Project/Artifact | Run Engine, object storage, workspace policy |
| Cost/Budget | Model/tool usage instrumentation |
| Evaluation gate | Immutable releases, trace metadata |
| Library update | Versioning của Agent/Workflow/Skill/Knowledge |

---

## 13. Workstream xuyên suốt

### 13.1. Security

- Cross-workspace isolation test cho mọi resource mới.
- Credential redaction test.
- SSRF/egress tests cho Tool/MCP/Webhook.
- Prompt/tool injection tests.
- Approval authorization và payload immutability.
- Dependency/supply-chain review cho Library.

### 13.2. QA

- Unit test domain/state machine.
- API contract tests.
- Database integration tests.
- Worker crash/recovery tests.
- E2E draft → publish → execute.
- Performance tests cho queue/run/event streaming.
- Visual regression và accessibility smoke tests.

### 13.3. UX

- Loading skeleton, progress và empty/error state.
- Reduced motion.
- Keyboard navigation cho canvas/dialog/table.
- Unsaved changes và version conflict UX.
- Confirmation rõ payload với approval/write actions.
- Consistent terminology Agent/Tool/Skill/Workflow/Run/Project.

### 13.4. Operations

- Feature flags theo capability.
- Migration/deploy/rollback runbook.
- Worker scaling và queue dashboard.
- Backup/restore cho PostgreSQL và MinIO.
- Retention jobs.
- Kill switch cho model/tool/workflow/schedule/release.

---

## 14. Definition of Done chung

Một capability chỉ được xem là hoàn thành khi:

- [ ] Có migration và forward-fix/rollback strategy.
- [ ] Có authorization ở server và cross-workspace test.
- [ ] Có unit, integration và E2E happy path.
- [ ] Có timeout, cancellation và error handling phù hợp.
- [ ] Có audit cho action thay đổi cấu hình/quyền/publish.
- [ ] Không có credential trong client, prompt, event, log hoặc trace.
- [ ] Có metrics và trace ID.
- [ ] API/event schema được version hóa hoặc có compatibility test.
- [ ] UI có loading, empty, error và success state.
- [ ] UI đạt keyboard accessibility và reduced motion baseline.
- [ ] Docker build, backend test, RAG test và frontend build đạt.
- [ ] Có runbook/support note cho capability vận hành.

---

## 15. Milestone gates

### Gate F0 — Foundation Ready

- Migration versioned hoạt động trên database hiện tại.
- Run/Step/Event và worker recovery đạt test.
- Idempotency/cancellation/SSE hoạt động.
- Không còn secret trong runtime payload.

### Gate A1 — Agent Ready

- Draft/test/publish/clone hoàn chỉnh.
- Production chat pin exact Agent release.
- Capability và permission validation đạt.

### Gate T1 — Tool/Skill Ready

- HTTP và MCP Tool chạy qua credential reference.
- SSRF, timeout, schema và redaction test đạt.
- Skill publish và dependency validation hoạt động.

### Gate W1 — Workflow MVP Ready

- Designer tạo được workflow hợp lệ.
- Agent/Tool/Condition/Approval chạy end-to-end.
- Restart/retry/cancel không gây duplicate effect.

### Gate P1 — Automation Ready

- Manual/cron/webhook tạo run an toàn.
- Project và artifact lineage hoàn chỉnh.

### Gate O1 — Operable

- Token/cost/latency/error truy vết tới step.
- Budget, alert và evaluation gate hoạt động.

### Gate L1 — Library Ready

- Publish/share/install/update/fork end-to-end.
- Permission expansion yêu cầu re-consent.
- Suspend/uninstall revoke capability đúng SLA.

---

## 16. Risk register

| ID | Rủi ro | Ảnh hưởng | Giảm thiểu |
|---|---|---:|---|
| R01 | Làm Designer trước Run Engine | Cao | Hoàn thành Gate F0 trước Workflow UI |
| R02 | Migration làm hỏng database hiện tại | Rất cao | Snapshot, rehearsal và integration migration tests |
| R03 | Retry tạo side effect trùng | Rất cao | Idempotency key, reconciliation và tool execution record |
| R04 | Secret lọt vào prompt/log/event | Rất cao | JIT resolution, structured redaction và leak tests |
| R05 | Agent/Workflow thay đổi âm thầm | Cao | Immutable published release và exact version pin |
| R06 | MCP/HTTP gây SSRF hoặc egress ngoài ý muốn | Rất cao | Allowlist, DNS/IP validation và outbound proxy policy |
| R07 | Worker restart làm mất execution | Cao | Persisted state, lease/heartbeat và recovery tests |
| R08 | Approval chỉ là UI, payload vẫn đổi sau duyệt | Rất cao | Hash/snapshot payload và re-authorization |
| R09 | Event/log tăng dữ liệu quá nhanh | Trung bình/Cao | Retention, payload references và partitioning |
| R10 | Observability làm sau nên thiếu dữ liệu | Cao | Telemetry field là DoD từ Sprint 2 |
| R11 | Library update phá dependency | Cao | Compatibility check, diff, pin và rollback |
| R12 | Scope 15 sprint vượt capacity | Cao | Gate funding, feature flags và cắt scope theo thứ tự |

---

## 17. Backlog đề xuất cho Sprint 1

### Epic FND-01 — Migration framework

- [ ] Chọn migration library/format.
- [ ] Tạo bảng migration metadata.
- [ ] Chuyển schema hiện tại thành baseline migration.
- [ ] Tạo `migrate status` và `migrate up`.
- [ ] Thêm migration lock.
- [ ] Viết migration integration tests.
- [ ] Viết deploy/rollback runbook.

### Epic FND-02 — Domain extraction

- [ ] Viết ADR modular monolith boundaries.
- [ ] Tạo package `runs`, `agents`, `credentials` skeleton.
- [ ] Tách Agent repository/service khỏi HTTP handler.
- [ ] Chuẩn hóa domain error → API error mapping.
- [ ] Chuẩn hóa workspace authorization helper.
- [ ] Thêm dependency boundary test/lint rule nếu phù hợp.

### Epic FND-03 — Contracts

- [ ] Định nghĩa Run/Step/Event schema v1.
- [ ] Định nghĩa status transition table.
- [ ] Định nghĩa idempotency behavior.
- [ ] Định nghĩa event redaction policy.
- [ ] Định nghĩa SSE resume contract.
- [ ] Review contract với frontend, backend, AI và QA.

### Sprint 1 exit criteria

- Migration baseline chạy được trên bản sao database hiện tại.
- Backend khởi động và toàn bộ test hiện có đạt.
- Agent API vẫn tương thích.
- Run/Step/Event contract được review và khóa cho Sprint 2.
- Không phát sinh regression ở Knowledge Base.

---

## 18. Phạm vi có thể lùi khi thiếu nguồn lực

Ưu tiên giữ:

1. Migration và Run Engine.
2. Agent versioning.
3. Credential Vault và HTTP Tool.
4. Workflow Agent/Tool/Condition.
5. Approval, retry, timeout và audit.
6. Observability tối thiểu.

Có thể lùi:

1. MCP resources/prompts ngoài tools.
2. Skill assets phức tạp.
3. Delay dài ngày hoặc loop node.
4. Advanced Project environment promotion.
5. Recommendation/rating trong Library.
6. Auto-update artifact.
7. Marketplace analytics.

Không được cắt:

- Workspace isolation.
- Credential security.
- Idempotency cho side effect.
- Immutable release.
- Approval payload integrity.
- Audit và trace tối thiểu.
- Worker recovery.

---

## 19. Kết luận và bước tiếp theo

Knowledge Base hiện là capability hoàn chỉnh đầu tiên và có thể trở thành một artifact trong Library ở phase cuối. P0 foundation đã tạo migration versioned, Run/Step/Event contract, durable worker và run API; bước phát triển kế tiếp là **Giai đoạn 1 — Chuẩn hóa Agent**.

Các quyết định nền đã chốt trong P0:

1. Migration dùng version số tăng dần, checksum bất biến và baseline tương thích database hiện hữu.
2. Worker dùng PostgreSQL lease/outbox trước; chỉ bổ sung queue riêng khi có bằng chứng về nhu cầu scale.
3. Release của từng artifact dùng version tăng dần và immutable; nhãn semantic version có thể bổ sung ở lớp sản phẩm.
4. Tool đầu tiên ở P2 là HTTP/OpenAPI read-only, sau đó mới mở remote MCP read-only.

Backlog kế tiếp bắt đầu từ Sprint 3: tách Agent domain, bổ sung draft/publish/version/clone và dựng test console trên Run Engine vừa hoàn thành.
