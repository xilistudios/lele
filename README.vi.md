<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">

  <h1>Lele</h1>

  <p>Trợ lý AI cá nhân nhẹ và hiệu quả, viết bằng Go.</p>

  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>

  [中文](README.zh.md) | [日本語](README.ja.md) | [Português](README.pt-br.md) | **Tiếng Việt** | [Français](README.fr.md) | [Español](README.es.md) | [English](README.md)
</div>

---

Lele là một dự án độc lập tập trung vào việc mang đến một trợ lý AI thực tế với dung lượng nhỏ, thời gian khởi động nhanh và mô hình triển khai đơn giản.

Ngày nay, dự án đã phát triển vượt xa một bot CLI tối giản. Lele bao gồm runtime agent có thể cấu hình, cổng kết nối đa kênh, giao diện web, API client原生, tác vụ đã lên lịch, subagent và mô hình tự động hóa lấy workspace làm trung tâm.

## Tại Sao Chọn Lele

- Triển khai Go nhẹ với mức tiêu thụ tài nguyên thấp
- Đủ hiệu quả để chạy thoải mái trên các máy Linux và bo mạch cấu hình khiêm tốn
- Một dự án duy nhất cho CLI, kênh chat, giao diện web và tích hợp client cục bộ
- Định tuyến nhà cung cấp có thể cấu hình, hỗ trợ cả backend trực tiếp và tương thích OpenAI
- Thiết kế theo triết lý workspace-first với skills, bộ nhớ, tác vụ đã lên lịch và kiểm soát sandbox

## Khả Năng Hiện Tại

### Agent Runtime

- Chat CLI với `lele agent`
- Vòng lặp agent sử dụng công cụ với giới hạn lặp có thể cấu hình
- Đính kèm tệp trong luồng native/web
- Duy trì phiên làm việc và hỗ trợ phiên tạm thời
- Đặt tên agent, ràng buộc và dự phòng mô hình

### Giao Diện

- Sử dụng qua terminal với CLI
- Chế độ gateway cho các kênh chat
- Giao diện web tích hợp sẵn
- Kênh client native với API REST + WebSocket và ghép đôi PIN

### Tự Động Hóa

- Tác vụ đã lên lịch với `lele cron`
- Tác vụ định kỳ dựa trên heartbeat từ `HEARTBEAT.md`
- Subagent bất đồng bộ cho công việc được ủy quyền
- Hệ thống skills cho các quy trình làm việc tái sử dụng

### An Toàn Và Vận Hành

- Hỗ trợ giới hạn trong workspace
- Mẫu từ chối lệnh nguy hiểm cho công cụ exec
- Luồng phê duyệt cho các tác vụ nhạy cảm
- Nhật ký, lệnh trạng thái và quản lý cấu hình

## Trạng Thái Dự Án

Lele là một dự án độc lập đang phát triển tích cực.

Codebase hiện tại đã hỗ trợ:

- Luồng gateway kiểu production
- Đường dẫn client web/native
- Định tuyến đa nhà cung cấp có thể cấu hình
- Nhiều kênh nhắn tin
- Skills, subagent và tự động hóa đã lên lịch

Khoảng trống tài liệu chính là README cũ vẫn mô tả danh tính một fork trước đó và không khớp với bộ tính năng hiện tại. README này phản ánh dự án đúng như thực tế.

## Bắt Đầu Nhanh

### Cài Đặt Từ Nguồn

```bash
git clone https://github.com/xilistudios/lele.git
cd lele
make deps
make build
```

Binary được ghi vào `build/lele`.

### Thiết Lập Ban Đầu

```bash
lele onboard
```

`onboard` tạo cấu hình cơ bản, template workspace và có thể tùy chọn bật giao diện web cũng như tạo PIN ghép đôi cho luồng client native/web.

### Sử Dụng CLI Tối Thiểu

```bash
lele agent -m "Bạn có thể làm gì?"
```

## Giao Diện Web Và Luồng Client Native

Lele hiện bao gồm giao diện web cục bộ cùng kênh client native.

Luồng điển hình:

1. Chạy `lele onboard`
2. Bật Web UI khi được nhắc
3. Tạo PIN ghép đôi
4. Khởi động dịch vụ với `lele gateway` và `lele web start`
5. Mở ứng dụng web trên trình duyệt và ghép đôi bằng PIN

Kênh native cung cấp các endpoint REST và WebSocket cho client desktop và tích hợp cục bộ.

Xem `docs/client-api.md` để biết API đầy đủ.

## Cấu Hình

Tệp cấu hình chính:

```text
~/.lele/config.json
```

Template cấu hình mẫu:

```text
config/config.example.json
```

Các khu vực cốt lõi bạn có thể cấu hình:

- `agents.defaults`: workspace, nhà cung cấp, mô hình, giới hạn token, giới hạn công cụ
- `session`: hành vi phiên tạm thời và liên kết danh tính
- `channels`: tích hợp gateway và nhắn tin
- `providers`: nhà cung cấp trực tiếp và backend tương thích OpenAI được đặt tên
- `tools`: tìm kiếm web, cài đặt an toàn cron và exec
- `heartbeat`: thực thi tác vụ định kỳ
- `gateway`, `logs`, `devices`

### Ví Dụ Tối Thiểu

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "restrict_to_workspace": true,
      "model": "glm-4.7",
      "max_tokens": 8192,
      "max_tool_iterations": 20
    }
  },
  "providers": {
    "openrouter": {
      "type": "openrouter",
      "api_key": "YOUR_API_KEY"
    }
  }
}
```

## Nhà Cung Cấp (Providers)

Lele hỗ trợ cả nhà cung cấp tích hợp sẵn và định nghĩa nhà cung cấp có tên.

Các họ nhà cung cấp tích hợp sẵn hiện có trong cấu hình/runtime bao gồm:

- `anthropic`
- `openai`
- `openrouter`
- `groq`
- `zhipu`
- `gemini`
- `vllm`
- `nvidia`
- `ollama`
- `moonshot`
- `deepseek`
- `github_copilot`

Dự án cũng hỗ trợ các mục nhà cung cấp tương thích OpenAI được đặt tên với cài đặt theo mô hình như:

- `model`
- `context_window`
- `max_tokens`
- `temperature`
- `vision`
- `reasoning`

## Kênh (Channels)

Gateway hiện bao gồm cấu hình cho:

- `telegram`
- `discord`
- `whatsapp`
- `feishu`
- `slack`
- `line`
- `onebot`
- `qq`
- `dingtalk`
- `maixcam`
- `native`
- `web`

Một số kênh là tích hợp dựa trên token đơn giản, trong khi số khác yêu cầu thiết lập webhook hoặc bridge.

## Bố Cục Workspace

Workspace mặc định:

```text
~/.lele/workspace/
```

Nội dung điển hình:

```text
~/.lele/workspace/
├── sessions/
├── memory/
├── state/
├── cron/
├── skills/
├── AGENT.md
├── HEARTBEAT.md
├── IDENTITY.md
├── SOUL.md
└── USER.md
```

Bố cục lấy workspace làm trung tâm này là một phần giúp Lele trở nên thực tế và hiệu quả: trạng thái, prompts, skills và tự động hóa đều nằm ở một nơi dễ dự đoán.

## Lên Lịch, Skills Và Subagents

### Tác Vụ Đã Lên Lịch

Sử dụng `lele cron` để tạo tác vụ một lần hoặc định kỳ.

Ví dụ:

```bash
lele cron list
lele cron add --name reminder --message "Kiểm tra sao lưu" --every "2h"
```

### Heartbeat

Lele có thể định kỳ đọc `HEARTBEAT.md` từ workspace và tự động thực thi tác vụ.

### Skills

Các skills tích hợp sẵn và tùy chỉnh có thể được quản lý với:

```bash
lele skills list
lele skills search
lele skills install <skill>
```

### Subagents

Lele hỗ trợ công việc bất đồng bộ được ủy quyền thông qua subagent. Điều này hữu ích cho các tác vụ chạy dài hoặc có thể song song hóa.

Xem `docs/SKILL_SUBAGENTS.md` để biết chi tiết.

## Mô Hình Bảo Mật

Lele có thể giới hạn quyền truy cập tệp và lệnh của agent vào workspace đã cấu hình.

Các kiểm soát chính bao gồm:

- `restrict_to_workspace`
- Mẫu từ chối exec
- Luồng phê duyệt cho tác vụ nhạy cảm
- Xác thực dựa trên token cho client native
- Giới hạn tải lên và TTL cho tệp tải lên native

Xem `docs/tools_configuration.md` và `docs/client-api.md` để biết chi tiết vận hành.

## Tham Khảo CLI

| Command | Description |
| --- | --- |
| `lele onboard` | Khởi tạo cấu hình và workspace |
| `lele agent` | Bắt đầu phiên agent tương tác |
| `lele agent -m "..."` | Chạy prompt một lần |
| `lele gateway` | Khởi động gateway nhắn tin |
| `lele web start` | Khởi động giao diện web tích hợp |
| `lele web status` | Hiển thị trạng thái giao diện web |
| `lele auth login` | Xác thực các nhà cung cấp được hỗ trợ |
| `lele status` | Hiển thị trạng thái runtime |
| `lele cron list` | Liệt kê tác vụ đã lên lịch |
| `lele cron add ...` | Thêm tác vụ đã lên lịch |
| `lele skills list` | Liệt kê skills đã cài đặt |
| `lele client pin` | Tạo PIN ghép đôi |
| `lele client list` | Liệt kê client native đã ghép đôi |
| `lele version` | Hiển thị thông tin phiên bản |

## Tài Liệu Bổ Sung

- `docs/agents-models-providers.md`
- `docs/architecture.md`
- `docs/channel-setup.md`
- `docs/cli-reference.md`
- `docs/config-reference.md`
- `docs/client-api.md`
- `docs/deployment.md`
- `docs/examples.md`
- `docs/installation-and-onboarding.md`
- `docs/logging-and-observability.md`
- `docs/model-routing.md`
- `docs/security-and-sandbox.md`
- `docs/session-and-workspace.md`
- `docs/skills-authoring.md`
- `docs/tools_configuration.md`
- `docs/troubleshooting.md`
- `docs/web-ui.md`
- `docs/SKILL_SUBAGENTS.md`
- `docs/SYSTEM_SPAWN_IMPLEMENTATION.md`

## Phát Triển

Các target hữu ích:

```bash
make build
make test
make fmt
make vet
make check
```

## Giấy Phép

MIT
