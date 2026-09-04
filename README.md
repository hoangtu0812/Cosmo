<p align="center">
  <img src="frontend/public/cosmo-logo.png" alt="Cosmo" width="180" />
</p>

<h1 align="center">Cosmo Enterprise AI Platform</h1>

<p align="center">
  Nền tảng AI nội bộ cho doanh nghiệp, tập trung vào xác thực thật, phân tách workspace và trò chuyện qua Model Gateway do tổ chức kiểm soát.
</p>

## Trạng thái hiện tại

Repository chứa vertical slice đầu tiên có thể chạy end-to-end:

- Đăng ký và đăng nhập bằng tài khoản Cosmo; mật khẩu được băm trước khi lưu.
- Đăng nhập doanh nghiệp bằng Microsoft Entra ID (OIDC) khi được cấu hình.
- Tự động bootstrap tài khoản quản trị từ biến môi trường.
- Phiên đăng nhập bằng JWT trong cookie `HttpOnly`.
- Chọn workspace cá nhân hoặc workspace doanh nghiệp theo quyền thành viên.
- Tự động mở workspace gần nhất sau đăng nhập; chỉ hiển thị màn chọn workspace khi cần đổi ngữ cảnh.
- Tạo, lưu và mở lại lịch sử hội thoại trong PostgreSQL.
- Stream câu trả lời từ API tương thích OpenAI; API key chỉ tồn tại ở backend.
- Giao diện responsive cho đăng nhập, workspace và chat, sử dụng Astryx Neutral cùng font Figtree tự host.

## Kiến trúc

```text
Browser (React 19 / Vinext / Astryx)
          |
          | REST + streaming
          v
Go API (Chi) -------- Microsoft Entra ID
   |      |
   |      +----------- OpenAI-compatible Model Gateway
   v
PostgreSQL 17
```

Các tích hợp MCP đi qua một lớp client giao thức chung, không chứa logic SAP.
Mỗi MCP server tự công bố tools và JSON Schema của nó; Cosmo lưu nguyên contract
đó và chọn một profile xác thực độc lập cho từng kết nối:

- `none`, bearer token hoặc custom header cho các server tương ứng;
- OAuth client credentials cho mọi authorization server hỗ trợ grant này;
- Authorization Code + PKCE theo RFC 9728/RFC 8414 cho danh tính người dùng,
  với token mã hóa riêng theo từng tool và từng người;
- Microsoft Entra on-behalf-of chỉ là adapter tương thích cho tích hợp cũ.

Các profile xác thực không làm thay đổi MCP transport hay tool schema.

Vì vậy thêm một MCP server mới không cần sửa Cosmo theo nghiệp vụ của server,
và SAP-MCP không cần biết model, agent hoặc cấu trúc nội bộ của Cosmo.

Checklist hợp đồng, cấu hình, conformance test trung lập và hướng dẫn kết nối
SAP-MCP nằm tại [docs/mcp-integration.md](docs/mcp-integration.md).

Ứng dụng được đóng gói bằng Docker Compose thành ba service: `frontend`, `backend` và `db`.

## Yêu cầu

- Docker Desktop có Docker Compose v2.
- PowerShell nếu sử dụng các script trong thư mục `scripts`.
- Tùy chọn cho phát triển không dùng container: Go 1.26+ và Node.js 22.13+.

## Chạy local

1. Tạo cấu hình local từ file mẫu:

   ```powershell
   Copy-Item .env.example .env
   ```

2. Thay toàn bộ giá trị mẫu của `POSTGRES_PASSWORD`, `SESSION_SECRET` và `ADMIN_PASSWORD` trong `.env`. `SESSION_SECRET` nên là chuỗi ngẫu nhiên tối thiểu 32 ký tự.

3. Khởi động hệ thống:

   ```powershell
   .\scripts\start-local.ps1
   ```

4. Mở [http://localhost:3100](http://localhost:3100). API health check có tại [http://localhost:8080/api/health](http://localhost:8080/api/health).

Dừng các container bằng:

```powershell
.\scripts\stop-local.ps1
```

Để xoá toàn bộ dữ liệu local (PostgreSQL, Qdrant và MinIO) trước khi khởi
động lại, chạy:

```powershell
.\scripts\start-local.ps1 -ResetData
```

Lệnh này không thể khôi phục dữ liệu đã xoá.

Dữ liệu PostgreSQL được giữ trong Docker volume `cosmo-postgres` sau khi dừng ứng dụng.

## Cấu hình Microsoft Entra ID

1. Tạo một **App registration** trong Microsoft Entra ID.
2. Chọn nền tảng **Web** và thêm redirect URI:

   ```text
   http://localhost:8080/api/auth/entra/callback
   ```

3. Tạo client secret và điền các biến sau trong `.env`:

   ```dotenv
   AZURE_AD_TENANT_ID=your-tenant-id
   AZURE_AD_CLIENT_ID=your-client-id
   AZURE_AD_CLIENT_SECRET=your-client-secret
   AZURE_AD_REDIRECT_URL=http://localhost:8080/api/auth/entra/callback
   ```

   Khi bật Entra ID, Cosmo chỉ cho phép đăng nhập bằng Microsoft; đăng ký và
   đăng nhập bằng email/mật khẩu cục bộ sẽ tự tắt. Thêm delegated Microsoft
   Graph permission `User.Read` (và cấp consent theo chính sách tenant) để
   Cosmo hiển thị ảnh đại diện của người dùng.

   Để cấp quyền quản trị hệ thống cho tài khoản Entra, thêm email vào
   `ADMIN_EMAILS` (nhiều email cách nhau bằng dấu phẩy), sau đó khởi động lại
   backend. Tài khoản sẽ có mục **Trang quản trị** trong card hồ sơ sau khi
   đăng nhập.

4. Build và khởi động lại backend. Nút “Tiếp tục với Microsoft” sẽ được bật khi cấu hình hợp lệ.

## Cấu hình Model Gateway

Cosmo kết nối tới endpoint hỗ trợ giao thức OpenAI Chat Completions. Có thể đặt trước LiteLLM, Azure OpenAI facade, vLLM, Ollama hoặc gateway nội bộ:

```dotenv
LLM_BASE_URL=https://your-gateway.example.com/v1
LLM_API_KEY=your-api-key
LLM_MODEL=company-general
LLM_REQUEST_TIMEOUT=90s
```

Không đặt `LLM_API_KEY` trong biến môi trường frontend. Trình duyệt chỉ giao tiếp với Go API.

## Lệnh kiểm tra

Backend:

```powershell
cd backend
go test ./...
```

Frontend:

```powershell
cd frontend
npm ci
npm run lint
npm run build
```

Toàn hệ thống:

```powershell
docker compose config --quiet
docker compose up -d --build
docker compose ps
```

## Cấu trúc repository

```text
backend/             Go API, auth, workspace, chat và PostgreSQL repository
frontend/            React UI và tài nguyên nhận diện Cosmo
scripts/             Script khởi động/dừng môi trường local
docker-compose.yml   Cấu hình ba service local
.env.example         Danh sách biến môi trường, chỉ chứa giá trị mẫu
```

## Bảo mật

- `.env`, credential, private key và certificate local bị loại khỏi Git bằng `.gitignore`.
- Ba tài liệu kiến trúc/kế hoạch nội bộ ban đầu cũng không được publish trong repository.
- Không commit secret thật. Nếu một secret từng được commit, hãy thu hồi/rotate secret đó; chỉ xóa file khỏi lịch sử Git là chưa đủ.
- Trước khi triển khai production, bật HTTPS, đặt `COOKIE_SECURE=true`, dùng secret manager và giới hạn CORS/redirect URI theo domain chính thức.

## Phạm vi tiếp theo

Các mốc phù hợp sau vertical slice này là quản trị thành viên/role, ingest tài liệu, RAG có trích dẫn, audit trail, policy enforcement, đánh giá chất lượng và quan sát chi phí/model.
