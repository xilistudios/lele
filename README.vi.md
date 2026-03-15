<div align="center">
<img src="assets/logo.jpg" alt="Lele" width="512">

<h1>Lele: Trợ lý AI Siêu Nhẹ viết bằng Go</h1>

<h3>Phần cứng $10 · RAM 10MB · Khởi động 1 giây · 皮皮虾，我们走！</h3>

  <p>
    <img src="https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/Arch-x86__64%2C%20ARM64%2C%20RISC--V-blue" alt="Hardware">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
    <br>
    <a href="https://lele.io"><img src="https://img.shields.io/badge/Website-lele.io-blue?style=flat&logo=google-chrome&logoColor=white" alt="Website"></a>
    <a href="https://x.com/SipeedIO"><img src="https://img.shields.io/badge/X_(Twitter)-SipeedIO-black?style=flat&logo=x&logoColor=white" alt="Twitter"></a>
  </p>

[中文](README.zh.md) | [日本語](README.ja.md) | [Português](README.pt-br.md) | **Tiếng Việt** | [Français](README.fr.md) | [English](README.md)
</div>

---

🦐 **Lele** là trợ lý AI cá nhân siêu nhẹ, lấy cảm hứng từ [nanobot](https://github.com/HKUDS/nanobot), được viết lại hoàn toàn bằng **Go** thông qua quá trình "tự khởi tạo" (self-bootstrapping) — nơi chính AI Agent đã tự dẫn dắt toàn bộ quá trình chuyển đổi kiến trúc và tối ưu hóa mã nguồn.

⚡️ **Cực kỳ nhẹ:** Chạy trên phần cứng chỉ **$10** với RAM **<10MB**. Tiết kiệm 99% bộ nhớ so với OpenClaw và rẻ hơn 98% so với Mac mini!

<table align="center">
<tr align="center">
<td align="center" valign="top">
<p align="center">
<img src="assets/picoclaw_mem.gif" width="360" height="240">
</p>
</td>
<td align="center" valign="top">
<p align="center">
<img src="assets/licheervnano.png" width="400" height="240">
</p>
</td>
</tr>
</table>

> [!CAUTION]
> **🚨 TUYÊN BỐ BẢO MẬT & KÊNH CHÍNH THỨC**
>
> * **KHÔNG CÓ CRYPTO:** Lele **KHÔNG** có bất kỳ token/coin chính thức nào. Mọi thông tin trên `pump.fun` hoặc các sàn giao dịch khác đều là **LỪA ĐẢO**.
> * **DOMAIN CHÍNH THỨC:** Website chính thức **DUY NHẤT** là **[lele.io](https://lele.io)**, website công ty là **[sipeed.com](https://sipeed.com)**.
> * **Cảnh báo:** Nhiều tên miền `.ai/.org/.com/.net/...` đã bị bên thứ ba đăng ký, không phải của chúng tôi.
> * **Cảnh báo:** Lele đang trong giai đoạn phát triển sớm và có thể còn các vấn đề bảo mật mạng chưa được giải quyết. Không nên triển khai lên môi trường production trước phiên bản v1.0.
> * **Lưu ý:** Lele gần đây đã merge nhiều PR, dẫn đến bộ nhớ sử dụng có thể lớn hơn (10–20MB) ở các phiên bản mới nhất. Chúng tôi sẽ ưu tiên tối ưu tài nguyên khi bộ tính năng đã ổn định.


## 📢 Tin tức

2026-02-16 🎉 Lele đạt 12K stars chỉ trong một tuần! Cảm ơn tất cả mọi người! Lele đang phát triển nhanh hơn chúng tôi tưởng tượng. Do số lượng PR tăng cao, chúng tôi cấp thiết cần maintainer từ cộng đồng. Các vai trò tình nguyện viên và roadmap đã được công bố [tại đây](docs/lele_community_roadmap_260216.md) — rất mong đón nhận sự tham gia của bạn!

2026-02-13 🎉 Lele đạt 5000 stars trong 4 ngày! Cảm ơn cộng đồng! Chúng tôi đang hoàn thiện **Lộ trình dự án (Roadmap)** và thiết lập **Nhóm phát triển** để đẩy nhanh tốc độ phát triển Lele.  
🚀 **Kêu gọi hành động:** Vui lòng gửi yêu cầu tính năng tại GitHub Discussions. Chúng tôi sẽ xem xét và ưu tiên trong cuộc họp hàng tuần.

2026-02-09 🎉 Lele chính thức ra mắt! Được xây dựng trong 1 ngày để mang AI Agent đến phần cứng $10 với RAM <10MB. 🦐 Lele, Lên Đường!

## ✨ Tính năng nổi bật

🪶 **Siêu nhẹ**: Bộ nhớ sử dụng <10MB — nhỏ hơn 99% so với Clawdbot (chức năng cốt lõi).

💰 **Chi phí tối thiểu**: Đủ hiệu quả để chạy trên phần cứng $10 — rẻ hơn 98% so với Mac mini.

⚡️ **Khởi động siêu nhanh**: Nhanh gấp 400 lần, khởi động trong 1 giây ngay cả trên CPU đơn nhân 0.6GHz.

🌍 **Di động thực sự**: Một file binary duy nhất chạy trên RISC-V, ARM và x86. Một click là chạy!

🤖 **AI tự xây dựng**: Triển khai Go-native tự động — 95% mã nguồn cốt lõi được Agent tạo ra, với sự tinh chỉnh của con người.

|                               | OpenClaw      | NanoBot                  | **Lele**                              |
| ----------------------------- | ------------- | ------------------------ | ----------------------------------------- |
| **Ngôn ngữ**                  | TypeScript    | Python                   | **Go**                                    |
| **RAM**                       | >1GB          | >100MB                   | **< 10MB**                                |
| **Thời gian khởi động**</br>(CPU 0.8GHz) | >500s         | >30s                     | **<1s**                                   |
| **Chi phí**                   | Mac Mini $599 | Hầu hết SBC Linux ~$50  | **Mọi bo mạch Linux**</br>**Chỉ từ $10** |

<img src="assets/compare.jpg" alt="Lele" width="512">

## 🦾 Demo

### 🛠️ Quy trình trợ lý tiêu chuẩn

<table align="center">
<tr align="center">
<th><p align="center">🧩 Lập trình Full-Stack</p></th>
<th><p align="center">🗂️ Quản lý Nhật ký & Kế hoạch</p></th>
<th><p align="center">🔎 Tìm kiếm Web & Học hỏi</p></th>
</tr>
<tr>
<td align="center"><p align="center"><img src="assets/picoclaw_code.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="assets/picoclaw_memory.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="assets/picoclaw_search.gif" width="240" height="180"></p></td>
</tr>
<tr>
<td align="center">Phát triển • Triển khai • Mở rộng</td>
<td align="center">Lên lịch • Tự động hóa • Ghi nhớ</td>
<td align="center">Khám phá • Phân tích • Xu hướng</td>
</tr>
</table>

### 🐜 Triển khai sáng tạo trên phần cứng tối thiểu

Lele có thể triển khai trên hầu hết mọi thiết bị Linux!

* $9.9 [LicheeRV-Nano](https://www.aliexpress.com/item/1005006519668532.html) phiên bản E (Ethernet) hoặc W (WiFi6), dùng làm Trợ lý Gia đình tối giản.
* $30~50 [NanoKVM](https://www.aliexpress.com/item/1005007369816019.html), hoặc $100 [NanoKVM-Pro](https://www.aliexpress.com/item/1005010048471263.html), dùng cho quản trị Server tự động.
* $50 [MaixCAM](https://www.aliexpress.com/item/1005008053333693.html) hoặc $100 [MaixCAM2](https://www.kickstarter.com/projects/zepan/maixcam2-build-your-next-gen-4k-ai-camera), dùng cho Giám sát thông minh.

https://private-user-images.githubusercontent.com/83055338/547056448-e7b031ff-d6f5-4468-bcca-5726b6fecb5c.mp4

🌟 Nhiều hình thức triển khai hơn đang chờ bạn khám phá!

## 📦 Cài đặt

### Cài đặt bằng binary biên dịch sẵn

Tải file binary cho nền tảng của bạn từ [trang Release](https://github.com/xilistudios/lele/releases).

### Cài đặt từ mã nguồn (có tính năng mới nhất, khuyên dùng cho phát triển)

```bash
git clone https://github.com/xilistudios/lele.git

cd lele
make deps

# Build (không cần cài đặt)
make build

# Build cho nhiều nền tảng
make build-all

# Build và cài đặt
make install
```

## 🐳 Docker Compose

Bạn cũng có thể chạy Lele bằng Docker Compose mà không cần cài đặt gì trên máy.

```bash
# 1. Clone repo
git clone https://github.com/xilistudios/lele.git
cd lele

# 2. Thiết lập API Key
cp config/config.example.json config/config.json
vim config/config.json      # Thiết lập DISCORD_BOT_TOKEN, API keys, v.v.

# 3. Build & Khởi động
docker compose --profile gateway up -d

# 4. Xem logs
docker compose logs -f picoclaw-gateway

# 5. Dừng
docker compose --profile gateway down
```

### Chế độ Agent (chạy một lần)

```bash
# Đặt câu hỏi
docker compose run --rm picoclaw-agent -m "2+2 bằng mấy?"

# Chế độ tương tác
docker compose run --rm picoclaw-agent
```

### Build lại

```bash
docker compose --profile gateway build --no-cache
docker compose --profile gateway up -d
```

### 🚀 Bắt đầu nhanh

> [!TIP]
> Thiết lập API key trong `~/.lele/config.json`.
> Lấy API key: [OpenRouter](https://openrouter.ai/keys) (LLM) · [Zhipu](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) (LLM)
> Tìm kiếm web là **tùy chọn** — lấy [Brave Search API](https://brave.com/search/api) miễn phí (2000 truy vấn/tháng) hoặc dùng tính năng auto fallback tích hợp sẵn.

**1. Khởi tạo**

```bash
picoclaw onboard
```

**2. Cấu hình** (`~/.lele/config.json`)

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "model": "glm-4.7",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20
    }
  },
  "providers": {
    "openrouter": {
      "api_key": "xxx",
      "api_base": "https://openrouter.ai/api/v1"
    }
  },
  "tools": {
    "web": {
      "brave": {
        "enabled": false,
        "api_key": "YOUR_BRAVE_API_KEY",
        "max_results": 5
      },
      "duckduckgo": {
        "enabled": true,
        "max_results": 5
      }
    }
  }
}
```

**3. Lấy API Key**

* **Nhà cung cấp LLM**: [OpenRouter](https://openrouter.ai/keys) · [Zhipu](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) · [Anthropic](https://console.anthropic.com) · [OpenAI](https://platform.openai.com) · [Gemini](https://aistudio.google.com/api-keys)
* **Tìm kiếm Web** (tùy chọn): [Brave Search](https://brave.com/search/api) — Có gói miễn phí (2000 truy vấn/tháng)

> **Lưu ý**: Xem `config.example.json` để có mẫu cấu hình đầy đủ.

**4. Trò chuyện**

```bash
picoclaw agent -m "Xin chào, bạn là ai?"
```

Vậy là xong! Bạn đã có một trợ lý AI hoạt động chỉ trong 2 phút.

---

## 💬 Tích hợp ứng dụng Chat

Trò chuyện với Lele qua Telegram, Discord, DingTalk hoặc LINE.

| Kênh | Mức độ thiết lập |
| --- | --- |
| **Telegram** | Dễ (chỉ cần token) |
| **Discord** | Dễ (bot token + intents) |
| **QQ** | Dễ (AppID + AppSecret) |
| **DingTalk** | Trung bình (app credentials) |
| **LINE** | Trung bình (credentials + webhook URL) |

<details>
<summary><b>Telegram</b> (Khuyên dùng)</summary>

**1. Tạo bot**

* Mở Telegram, tìm `@BotFather`
* Gửi `/newbot`, làm theo hướng dẫn
* Sao chép token

**2. Cấu hình**

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allow_from": ["YOUR_USER_ID"]
    }
  }
}
```

> Lấy User ID từ `@userinfobot` trên Telegram.

**3. Chạy**

```bash
picoclaw gateway
```

</details>

<details>
<summary><b>Discord</b></summary>

**1. Tạo bot**

* Truy cập <https://discord.com/developers/applications>
* Create an application → Bot → Add Bot
* Sao chép bot token

**2. Bật Intents**

* Trong phần Bot settings, bật **MESSAGE CONTENT INTENT**
* (Tùy chọn) Bật **SERVER MEMBERS INTENT** nếu muốn dùng danh sách cho phép theo thông tin thành viên

**3. Lấy User ID**

* Discord Settings → Advanced → bật **Developer Mode**
* Click chuột phải vào avatar → **Copy User ID**

**4. Cấu hình**

```json
{
  "channels": {
    "discord": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allow_from": ["YOUR_USER_ID"]
    }
  }
}
```

**5. Mời bot vào server**

* OAuth2 → URL Generator
* Scopes: `bot`
* Bot Permissions: `Send Messages`, `Read Message History`
* Mở URL mời được tạo và thêm bot vào server của bạn

**6. Chạy**

```bash
picoclaw gateway
```

</details>

<details>
<summary><b>QQ</b></summary>

**1. Tạo bot**

* Truy cập [QQ Open Platform](https://q.qq.com/#)
* Tạo ứng dụng → Lấy **AppID** và **AppSecret**

**2. Cấu hình**

```json
{
  "channels": {
    "qq": {
      "enabled": true,
      "app_id": "YOUR_APP_ID",
      "app_secret": "YOUR_APP_SECRET",
      "allow_from": []
    }
  }
}
```

> Để `allow_from` trống để cho phép tất cả người dùng, hoặc chỉ định số QQ để giới hạn quyền truy cập.

**3. Chạy**

```bash
picoclaw gateway
```

</details>

<details>
<summary><b>DingTalk</b></summary>

**1. Tạo bot**

* Truy cập [Open Platform](https://open.dingtalk.com/)
* Tạo ứng dụng nội bộ
* Sao chép Client ID và Client Secret

**2. Cấu hình**

```json
{
  "channels": {
    "dingtalk": {
      "enabled": true,
      "client_id": "YOUR_CLIENT_ID",
      "client_secret": "YOUR_CLIENT_SECRET",
      "allow_from": []
    }
  }
}
```

> Để `allow_from` trống để cho phép tất cả người dùng, hoặc chỉ định ID để giới hạn quyền truy cập.

**3. Chạy**

```bash
picoclaw gateway
```

</details>

<details>
<summary><b>LINE</b></summary>

**1. Tạo tài khoản LINE Official**

- Truy cập [LINE Developers Console](https://developers.line.biz/)
- Tạo provider → Tạo Messaging API channel
- Sao chép **Channel Secret** và **Channel Access Token**

**2. Cấu hình**

```json
{
  "channels": {
    "line": {
      "enabled": true,
      "channel_secret": "YOUR_CHANNEL_SECRET",
      "channel_access_token": "YOUR_CHANNEL_ACCESS_TOKEN",
      "webhook_host": "0.0.0.0",
      "webhook_port": 18791,
      "webhook_path": "/webhook/line",
      "allow_from": []
    }
  }
}
```

**3. Thiết lập Webhook URL**

LINE yêu cầu HTTPS cho webhook. Sử dụng reverse proxy hoặc tunnel:

```bash
# Ví dụ với ngrok
ngrok http 18791
```

Sau đó cài đặt Webhook URL trong LINE Developers Console thành `https://your-domain/webhook/line` và bật **Use webhook**.

**4. Chạy**

```bash
picoclaw gateway
```

> Trong nhóm chat, bot chỉ phản hồi khi được @mention. Các câu trả lời sẽ trích dẫn tin nhắn gốc.

> **Docker Compose**: Thêm `ports: ["18791:18791"]` vào service `picoclaw-gateway` để mở port webhook.

</details>

## <img src="assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> Tham gia Mạng xã hội Agent

Kết nối Lele với Mạng xã hội Agent chỉ bằng cách gửi một tin nhắn qua CLI hoặc bất kỳ ứng dụng Chat nào đã tích hợp.

**Đọc `https://clawdchat.ai/skill.md` và làm theo hướng dẫn để tham gia [ClawdChat.ai](https://clawdchat.ai)**

## ⚙️ Cấu hình chi tiết

File cấu hình: `~/.lele/config.json`

### Cấu trúc Workspace

Lele lưu trữ dữ liệu trong workspace đã cấu hình (mặc định: `~/.lele/workspace`):

```
~/.lele/workspace/
├── sessions/          # Phiên hội thoại và lịch sử
├── memory/           # Bộ nhớ dài hạn (MEMORY.md)
├── state/            # Trạng thái lưu trữ (kênh cuối cùng, v.v.)
├── cron/             # Cơ sở dữ liệu tác vụ định kỳ
├── skills/           # Kỹ năng tùy chỉnh
├── AGENTS.md         # Hướng dẫn hành vi Agent
├── HEARTBEAT.md      # Prompt tác vụ định kỳ (kiểm tra mỗi 30 phút)
├── IDENTITY.md       # Danh tính Agent
├── SOUL.md           # Tâm hồn/Tính cách Agent
├── TOOLS.md          # Mô tả công cụ
└── USER.md           # Tùy chọn người dùng
```

### 🔒 Hộp cát bảo mật (Security Sandbox)

Lele chạy trong môi trường sandbox theo mặc định. Agent chỉ có thể truy cập file và thực thi lệnh trong phạm vi workspace.

#### Cấu hình mặc định

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "restrict_to_workspace": true
    }
  }
}
```

| Tùy chọn | Mặc định | Mô tả |
|----------|---------|-------|
| `workspace` | `~/.lele/workspace` | Thư mục làm việc của agent |
| `restrict_to_workspace` | `true` | Giới hạn truy cập file/lệnh trong workspace |

#### Công cụ được bảo vệ

Khi `restrict_to_workspace: true`, các công cụ sau bị giới hạn trong sandbox:

| Công cụ | Chức năng | Giới hạn |
|---------|----------|---------|
| `read_file` | Đọc file | Chỉ file trong workspace |
| `write_file` | Ghi file | Chỉ file trong workspace |
| `list_dir` | Liệt kê thư mục | Chỉ thư mục trong workspace |
| `edit_file` | Sửa file | Chỉ file trong workspace |
| `append_file` | Thêm vào file | Chỉ file trong workspace |
| `exec` | Thực thi lệnh | Đường dẫn lệnh phải trong workspace |

#### Bảo vệ bổ sung cho Exec

Ngay cả khi `restrict_to_workspace: false`, công cụ `exec` vẫn chặn các lệnh nguy hiểm sau:

* `rm -rf`, `del /f`, `rmdir /s` — Xóa hàng loạt
* `format`, `mkfs`, `diskpart` — Định dạng ổ đĩa
* `dd if=` — Tạo ảnh đĩa
* Ghi vào `/dev/sd[a-z]` — Ghi trực tiếp lên đĩa
* `shutdown`, `reboot`, `poweroff` — Tắt/khởi động lại hệ thống
* Fork bomb `:(){ :|:& };:`

#### Ví dụ lỗi

```
[ERROR] tool: Tool execution failed
{tool=exec, error=Command blocked by safety guard (path outside working dir)}
```

```
[ERROR] tool: Tool execution failed
{tool=exec, error=Command blocked by safety guard (dangerous pattern detected)}
```

#### Tắt giới hạn (Rủi ro bảo mật)

Nếu bạn cần agent truy cập đường dẫn ngoài workspace:

**Cách 1: File cấu hình**

```json
{
  "agents": {
    "defaults": {
      "restrict_to_workspace": false
    }
  }
}
```

**Cách 2: Biến môi trường**

```bash
export PICOCLAW_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE=false
```

> ⚠️ **Cảnh báo**: Tắt giới hạn này cho phép agent truy cập mọi đường dẫn trên hệ thống. Chỉ sử dụng cẩn thận trong môi trường được kiểm soát.

#### Tính nhất quán của ranh giới bảo mật

Cài đặt `restrict_to_workspace` áp dụng nhất quán trên mọi đường thực thi:

| Đường thực thi | Ranh giới bảo mật |
|----------------|-------------------|
| Agent chính | `restrict_to_workspace` ✅ |
| Subagent / Spawn | Kế thừa cùng giới hạn ✅ |
| Tác vụ Heartbeat | Kế thừa cùng giới hạn ✅ |

Tất cả đường thực thi chia sẻ cùng giới hạn workspace — không có cách nào vượt qua ranh giới bảo mật thông qua subagent hoặc tác vụ định kỳ.

### Heartbeat (Tác vụ định kỳ)

Lele có thể tự động thực hiện các tác vụ định kỳ. Tạo file `HEARTBEAT.md` trong workspace:

```markdown
# Tác vụ định kỳ

- Kiểm tra email xem có tin nhắn quan trọng không
- Xem lại lịch cho các sự kiện sắp tới
- Kiểm tra dự báo thời tiết
```

Agent sẽ đọc file này mỗi 30 phút (có thể cấu hình) và thực hiện các tác vụ bằng công cụ có sẵn.

#### Tác vụ bất đồng bộ với Spawn

Đối với các tác vụ chạy lâu (tìm kiếm web, gọi API), sử dụng công cụ `spawn` để tạo **subagent**:

```markdown
# Tác vụ định kỳ

## Tác vụ nhanh (trả lời trực tiếp)
- Báo cáo thời gian hiện tại

## Tác vụ lâu (dùng spawn cho async)
- Tìm kiếm tin tức AI trên web và tóm tắt
- Kiểm tra email và báo cáo tin nhắn quan trọng
```

**Hành vi chính:**

| Tính năng | Mô tả |
|-----------|-------|
| **spawn** | Tạo subagent bất đồng bộ, không chặn heartbeat |
| **Context độc lập** | Subagent có context riêng, không có lịch sử phiên |
| **message tool** | Subagent giao tiếp trực tiếp với người dùng qua công cụ message |
| **Không chặn** | Sau khi spawn, heartbeat tiếp tục tác vụ tiếp theo |

#### Cách Subagent giao tiếp

```
Heartbeat kích hoạt
    ↓
Agent đọc HEARTBEAT.md
    ↓
Tác vụ lâu: spawn subagent
    ↓                           ↓
Tiếp tục tác vụ tiếp theo   Subagent làm việc độc lập
    ↓                           ↓
Tất cả tác vụ hoàn thành    Subagent dùng công cụ "message"
    ↓                           ↓
Phản hồi HEARTBEAT_OK       Người dùng nhận kết quả trực tiếp
```

Subagent có quyền truy cập các công cụ (message, web_search, v.v.) và có thể giao tiếp với người dùng một cách độc lập mà không cần thông qua agent chính.

**Cấu hình:**

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": 30
  }
}
```

| Tùy chọn | Mặc định | Mô tả |
|----------|---------|-------|
| `enabled` | `true` | Bật/tắt heartbeat |
| `interval` | `30` | Khoảng thời gian kiểm tra (phút, tối thiểu: 5) |

**Biến môi trường:**

* `PICOCLAW_HEARTBEAT_ENABLED=false` để tắt
* `PICOCLAW_HEARTBEAT_INTERVAL=60` để thay đổi khoảng thời gian

### Nhà cung cấp (Providers)

> [!NOTE]
> Groq cung cấp dịch vụ chuyển giọng nói thành văn bản miễn phí qua Whisper. Nếu đã cấu hình Groq, tin nhắn thoại trên Telegram sẽ được tự động chuyển thành văn bản.

| Nhà cung cấp | Mục đích | Lấy API Key |
| --- | --- | --- |
| `gemini` | LLM (Gemini trực tiếp) | [aistudio.google.com](https://aistudio.google.com) |
| `zhipu` | LLM (Zhipu trực tiếp) | [bigmodel.cn](bigmodel.cn) |
| `openrouter` (Đang thử nghiệm) | LLM (khuyên dùng, truy cập mọi model) | [openrouter.ai](https://openrouter.ai) |
| `anthropic` (Đang thử nghiệm) | LLM (Claude trực tiếp) | [console.anthropic.com](https://console.anthropic.com) |
| `openai` (Đang thử nghiệm) | LLM (GPT trực tiếp) | [platform.openai.com](https://platform.openai.com) |
| `deepseek` (Đang thử nghiệm) | LLM (DeepSeek trực tiếp) | [platform.deepseek.com](https://platform.deepseek.com) |
| `groq` | LLM + **Chuyển giọng nói** (Whisper) | [console.groq.com](https://console.groq.com) |

<details>
<summary><b>Cấu hình Zhipu</b></summary>

**1. Lấy API key**

* Lấy [API key](https://bigmodel.cn/usercenter/proj-mgmt/apikeys)

**2. Cấu hình**

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "model": "glm-4.7",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20
    }
  },
  "providers": {
    "zhipu": {
      "api_key": "Your API Key",
      "api_base": "https://open.bigmodel.cn/api/paas/v4"
    }
  }
}
```

**3. Chạy**

```bash
picoclaw agent -m "Xin chào"
```

</details>

<details>
<summary><b>Ví dụ cấu hình đầy đủ</b></summary>

```json
{
  "agents": {
    "defaults": {
      "model": "anthropic/claude-opus-4-5"
    }
  },
  "providers": {
    "openrouter": {
      "api_key": "sk-or-v1-xxx"
    },
    "groq": {
      "api_key": "gsk_xxx"
    }
  },
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "123456:ABC...",
      "allow_from": ["123456789"]
    },
    "discord": {
      "enabled": true,
      "token": "",
      "allow_from": [""]
    },
    "whatsapp": {
      "enabled": false
    },
    "feishu": {
      "enabled": false,
      "app_id": "cli_xxx",
      "app_secret": "xxx",
      "encrypt_key": "",
      "verification_token": "",
      "allow_from": []
    },
    "qq": {
      "enabled": false,
      "app_id": "",
      "app_secret": "",
      "allow_from": []
    }
  },
  "tools": {
    "web": {
      "brave": {
        "enabled": false,
        "api_key": "BSA...",
        "max_results": 5
      },
      "duckduckgo": {
        "enabled": true,
        "max_results": 5
      }
    }
  },
  "heartbeat": {
    "enabled": true,
    "interval": 30
  }
}
```

</details>

## Tham chiếu CLI

| Lệnh | Mô tả |
| --- | --- |
| `picoclaw onboard` | Khởi tạo cấu hình & workspace |
| `picoclaw agent -m "..."` | Trò chuyện với agent |
| `picoclaw agent` | Chế độ chat tương tác |
| `picoclaw gateway` | Khởi động gateway (cho bot chat) |
| `picoclaw status` | Hiển thị trạng thái |
| `picoclaw cron list` | Liệt kê tất cả tác vụ định kỳ |
| `picoclaw cron add ...` | Thêm tác vụ định kỳ |

### Tác vụ định kỳ / Nhắc nhở

Lele hỗ trợ nhắc nhở theo lịch và tác vụ lặp lại thông qua công cụ `cron`:

* **Nhắc nhở một lần**: "Remind me in 10 minutes" (Nhắc tôi sau 10 phút) → kích hoạt một lần sau 10 phút
* **Tác vụ lặp lại**: "Remind me every 2 hours" (Nhắc tôi mỗi 2 giờ) → kích hoạt mỗi 2 giờ
* **Biểu thức Cron**: "Remind me at 9am daily" (Nhắc tôi lúc 9 giờ sáng mỗi ngày) → sử dụng biểu thức cron

Các tác vụ được lưu trong `~/.lele/workspace/cron/` và được xử lý tự động.

## 🤝 Đóng góp & Lộ trình

Chào đón mọi PR! Mã nguồn được thiết kế nhỏ gọn và dễ đọc. 🤗

Lộ trình sắp được công bố...

Nhóm phát triển đang được xây dựng. Điều kiện tham gia: Ít nhất 1 PR đã được merge.

Nhóm người dùng:

Discord: <https://discord.gg/V4sAZ9XWpN>

<img src="assets/wechat.png" alt="Lele" width="512">

## 🐛 Xử lý sự cố

### Tìm kiếm web hiện "API 配置问题"

Điều này là bình thường nếu bạn chưa cấu hình API key cho tìm kiếm. Lele sẽ cung cấp các liên kết hữu ích để tìm kiếm thủ công.

Để bật tìm kiếm web:

1. **Tùy chọn 1 (Khuyên dùng)**: Lấy API key miễn phí tại [https://brave.com/search/api](https://brave.com/search/api) (2000 truy vấn miễn phí/tháng) để có kết quả tốt nhất.
2. **Tùy chọn 2 (Không cần thẻ tín dụng)**: Nếu không có key, hệ thống tự động chuyển sang dùng **DuckDuckGo** (không cần key).

Thêm key vào `~/.lele/config.json` nếu dùng Brave:

```json
{
  "tools": {
    "web": {
      "brave": {
        "enabled": true,
        "api_key": "YOUR_BRAVE_API_KEY",
        "max_results": 5
      },
      "duckduckgo": {
        "enabled": true,
        "max_results": 5
      }
    }
  }
}
```

### Gặp lỗi lọc nội dung (Content Filtering)

Một số nhà cung cấp (như Zhipu) có bộ lọc nội dung nghiêm ngặt. Thử diễn đạt lại câu hỏi hoặc sử dụng model khác.

### Telegram bot báo "Conflict: terminated by other getUpdates"

Điều này xảy ra khi có một instance bot khác đang chạy. Đảm bảo chỉ có một tiến trình `picoclaw gateway` chạy tại một thời điểm.

---

## 📝 So sánh API Key

| Dịch vụ | Gói miễn phí | Trường hợp sử dụng |
| --- | --- | --- |
| **OpenRouter** | 200K tokens/tháng | Đa model (Claude, GPT-4, v.v.) |
| **Zhipu** | 200K tokens/tháng | Tốt nhất cho người dùng Trung Quốc |
| **Brave Search** | 2000 truy vấn/tháng | Chức năng tìm kiếm web |
| **Groq** | Có gói miễn phí | Suy luận siêu nhanh (Llama, Mixtral) |
