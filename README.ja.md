<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">
  <img src="assets/tui.png" alt="TUI" width="650">

  <h1>Lele</h1>

  <p>軽量なパーソナルAIアシスタント（Go）— 単一バイナリ、小さいフットプリント、高速なTUI。</p>
  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/binary-~57%20MB-blue" alt="Binary size">
    <img src="https://img.shields.io/badge/TUI%20RSS-~42%20MB-success" alt="TUI RSS">
    <img src="https://img.shields.io/badge/render-~3.4%20ms%20%40%20200%C3%9750-success" alt="Render time">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>

  [中文](README.zh.md) | **日本語** | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Español](README.es.md) | [English](README.md)
</div>

---

LeleはワークスペースファーストのAIアシスタントで、長時間稼働するホストを想定しています。CLIおよびTUIチャット、マルチチャネルゲートウェイ、Web UI、ネイティブクライアントAPI、スキル、cron、サブエージェントを備え、JSランタイムやマルチプロセスのTUIスタックは不要です。

## クイックスタート

```bash
# Linux / macOS / BSD
curl -fsSL https://raw.githubusercontent.com/xilistudios/lele/main/install.sh | sh

# Windows (PowerShell)
irm https://raw.githubusercontent.com/xilistudios/lele/main/install.ps1 | iex
```
```bash
# ソースからビルド
git clone https://github.com/xilistudios/lele.git && cd lele
make deps && make build && make install
```
```
# 初期セットアップ
lele onboard
# CLIエージェント
lele agent -m "何ができますか？"
# TUI
lele tui
```

インストーラはSHA256を検証し、デフォルトでは `~/.local/bin` にインストールします。

## ベンチマーク

Apple M1 / macOS arm64で計測。アイドルTUIセッション（PTY下、モデル呼び出しなし）。**Render** は1フレーム全体を描画するまでの壁時計時間（lele：リポジトリ内の `View()` bench 200×50、その他：プロセス起動から最初の完全なTUI描画まで — init + 最初のフレーム）。RSSはその描画後約6秒のプロセストリーのピークメモリ。

| ツール | ランタイム | インストールサイズ | Render | アイドルTUI RSS | プロセス数 |
| --- | --- | --- | --- | --- | --- |
| **lele** | Go + Bubble Tea | **57 MB** 単一バイナリ | **≈ 3.4 ms/frame** (200×50 `View()`) | **~42 MB** | **1** |
| Claude Code 2.1.267 | Bun-compiled TS | 191 MB バイナリ | ~400 ms 初回描画まで | 170–187 MB | 1 |
| pi 0.85.1 | Bun-compiled TS + pi-tui | 71 MB バイナリ | ~350 ms 初回描画まで | ~173 MB | 1 |
| OpenCode 1.17.8 | Bun + OpenTUI | 123 MB バイナリ | ~950 ms 初回描画まで | ~1.0 GB ピーク | 1 |
| Hermes 0.14.0 | Python + Ink | ~1 GB ツリー | ~1.3 s 初回描画まで | ~374 MB | 3–4 |

### 他ツールとのリソース差

同一マシン、同一アイドルTUIセッション。lele（~42 MB RSS、~57 MB バイナリ、**~60 ms** 初回TUI描画）に対する概算倍率：

| vs | メモリ | ディスク / インストール | 初回レンダー | 備考 |
| --- | --- | --- | --- | --- |
| Claude Code | **~4× 少ない** | **~3× 小さい** | **~7× 高速** | 42 vs ~180 MB · 60 ms vs ~400 ms |
| pi | **~4× 少ない** | ~同等 | **~6× 高速** | 42 vs ~173 MB · 60 ms vs ~350 ms |
| OpenCode | **~25× 少ない** | **~2× 小さい** | **~16× 高速** | 42 vs ~1 GB · 60 ms vs ~950 ms |
| Hermes | **~9× 少ない** | **~17× 小さい** | **~22× 高速** | 42 vs ~374 MB · 60 ms vs ~1.3 s |

## 機能

**エージェントランタイム**
- 反復上限付きツール利用エージェントループ
- 名前付きエージェント、モデルフォールバック、エージェントごとの `thinking_level`（`/think` で上書き可）
- セッション永続化（SQLite）と任意のエフェメラルセッション
- ネイティブ/Webフローでのファイル添付

**インターフェース**
- CLI（`lele agent`）とフルBubble Tea TUI（`lele tui`）
- 内蔵Web UI + PINペアリングのネイティブREST/WebSocketクライアント
- チャットチャネル用ゲートウェイ（Telegram、Discord、Slack、WhatsApp、Feishu、Line、QQ、DingTalkなど）

**自動化**
- スケジュールジョブ（`lele cron`）と `HEARTBEAT.md` ハートビートタスク
- スキルと非同期サブエージェント
- マルチエージェントグループチャット（MoA / ラウンドロビン / モデレーター / パイプライン）— `docs/moa-group-chat.md` を参照

**安全性**
- ワークスペース制限、exec拒否パターン、承認フロー
- ネイティブクライアントのトークン認証、アップロード上限/TTL

## スクリーンショット

### TUI


![進行中のLele TUIチャット](assets/tui-chat.png)

```bash
lele tui              # 新しいセッション
lele tui -s <id>      # セッションの再開
```

ストリーミングチャット、ツール可視化、Markdown、セッションサイドバー、コンテキスト統計。テーマ：`dracula`（デフォルト）、`nord`、`catppuccin`、`gruvbox`、`tokyo-night`、`solarized-light` — **Settings → Interface** で切替、`~/.lele/tui.json` に保存。言語は `LELE_LANG` または `/lang` で設定。

### Web UI

![Lele Web UI](assets/webui.png)

![進行中のLele Web UIチャット](assets/webui-chat.png)

1. `lele onboard` — Web UIを有効化し、ペアリングPINを取得  
2. `lele gateway` — ポート `18790` で `/`、`/api/v1/*`、WebSocketを提供  
3. ブラウザで開き、PINでペアリング  

フルクライアントAPI：`docs/client-api.md`。


## 設定

設定は `~/.lele/config.json`（テンプレート：`config/config.example.json`）にあります。

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

主なセクション：`agents.defaults`、`session`、`channels`、`providers`、`tools`、`heartbeat`、`gateway`、`logs`、`devices`。

**プロバイダー：** `anthropic`、`openai`、`openrouter`、`groq`、`zhipu`、`gemini`、`vllm`、`nvidia`、`ollama`、`moonshot`、`deepseek`、`github_copilot`、および名前付きOpenAI互換バックエンド（`model`、`context_window`、`vision`、`reasoning` など）。

**ワークスペース**（`~/.lele/workspace/`）：`sessions/`、`memory/`、`state/`、`cron/`、`skills/`、`AGENT.md`、`HEARTBEAT.md`、`IDENTITY.md`、`SOUL.md`、`USER.md`。

## CLI

| コマンド | 説明 |
| --- | --- |
| `lele onboard` | 設定とワークスペースを初期化 |
| `lele agent` / `lele agent -m "..."` | 対話式またはワンショットエージェント |
| `lele tui` / `lele tui -s <session>` | ターミナルUI |
| `lele gateway` | メッセージングゲートウェイ + Web UI |
| `lele auth login` | プロバイダーを認証 |
| `lele status` | ランタイム状態 |
| `lele cron list` / `lele cron add ...` | スケジュールジョブ |
| `lele skills list` | インストール済みスキル |
| `lele client pin` / `lele client list` | ネイティブクライアントのペアリング |
| `lele version` | バージョン情報 |

## 開発

Go 1.25+、[Bun](https://bun.sh/)（Web UI）、Makeが必要です。

```bash
make deps web-build build   # バイナリ + web をビルド
make test fmt vet           # チェック
make build-all              # クロスコンパイル
```

## ライセンス

MIT
