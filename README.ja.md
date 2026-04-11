<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">

  <h1>Lele</h1>

  <p>Goで書かれた、軽量かつ効率的なパーソナルAIアシスタント。</p>

  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>

  [中文](README.zh.md) | **日本語** | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Español](README.es.md) | [English](README.md)
</div>

---

Leleは、実用的なAIアシスタントの実現を目指し、フットプリントの小ささ、高速な起動、そしてシンプルなデプロイモデルに焦点を当てた独立プロジェクトです。

現在のプロジェクトは、最小限のCLIボットにとどまりません。設定可能なエージェントランタイム、マルチチャネルゲートウェイ、Web UI、ネイティブクライアントAPI、スケジュールタスク、サブエージェント、ワークスペース中心のオートメーションモデルを備えています。

## なぜLeleなのか

- Goで実装された軽量なアーキテクチャと小さな運用フットプリント
- 低スペックなLinuxマシンやボードでも快適に動作する効率性
- CLI、チャットチャネル、Web UI、ローカルクライアント統合を1つのプロジェクトで提供
- ダイレクトおよびOpenAI互換バックエンドをサポートする、設定可能なプロバイダールーティング
- スキル、メモリ、スケジュールジョブ、サンドボックスコントロールを備えたワークスペースファースト設計

## 現在の機能

### エージェントランタイム

- `lele agent`によるCLIチャット
- 反復回数を設定可能なツール使用エージェントループ
- ネイティブ/Webフローでのファイル添付
- セッション永続化とオプションのエフェメラルセッション
- 名前付きエージェント、バインディング、モデルフォールバック

### インターフェース

- CLI経由のターミナル操作
- チャットチャネル用のゲートウェイモード
- 組み込みWeb UI
- REST + WebSocket APIとPINペアリングを備えたネイティブクライアントチャネル

### オートメーション

- `lele cron`によるスケジュールジョブ
- `HEARTBEAT.md`に基づく定期的なタスク
- 委譲作業のための非同期サブエージェント
- 再利用可能なワークフローのためのスキルシステム

### セキュリティと運用

- ワークスペースへの制限サポート
- execツールの危険コマンド拒否パターン
- 機密性の高いアクションへの承認フロー
- ログ、ステータスコマンド、設定管理

## プロジェクトの現状

Leleは積極的に開発が進められているスタンドアロンプロジェクトです。

現在のコードベースは以下をサポートしています:

- プロダクションレベルのゲートウェイフロー
- Web/ネイティブクライアントパス
- 設定可能なマルチプロバイダールーティング
- 複数のメッセージングチャネル
- スキル、サブエージェント、スケジュール自動化

主なドキュメントのギャップは、以前のREADMEが古いフォークのアイデンティティを記述しており、現在の機能セットと一致していなかったことです。このREADMEは、プロジェクトの現状を反映しています。

## クイックスタート

### ソースからインストール

```bash
git clone https://github.com/xilistudios/lele.git
cd lele
make deps
make build
```

バイナリは`build/lele`に出力されます。

### 初期セットアップ

```bash
lele onboard
```

`onboard`は基本設定、ワークステンプレートを作成し、オプションでWeb UIを有効にしてネイティブ/Webクライアントフロー用のペアリングPINを生成します。

### 最小限のCLI使用法

```bash
lele agent -m "What can you do?"
```

## Web UIとネイティブクライアントフロー

LeleにはローカルWeb UIとネイティブクライアントチャネルが含まれています。

一般的なフロー:

1. `lele onboard`を実行
2. プロンプトでWeb UIを有効化
3. ペアリングPINを生成
4. `lele gateway`と`lele web start`でサービスを起動
5. ブラウザでWebアプリを開き、PINでペアリング

ネイティブチャネルは、デスクトップクライアントやローカル統合向けのRESTおよびWebSocketエンドポイントを公開します。

完全なAPIについては`docs/client-api.md`を参照してください。

## 設定

メイン設定ファイル:

```text
~/.lele/config.json
```

設定テンプレートの例:

```text
config/config.example.json
```

設定可能な主要領域:

- `agents.defaults`: ワークスペース、プロバイダー、モデル、トークン制限、ツール制限
- `session`: エフェメラルセッションの動作とアイデンティティリンク
- `channels`: ゲートウェイおよびメッセージング統合
- `providers`: ダイレクトプロバイダーおよび名前付きOpenAI互換バックエンド
- `tools`: Web検索、cron、execの安全性設定
- `heartbeat`: 定期タスクの実行
- `gateway`、`logs`、`devices`

### 最小限の例

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

## プロバイダー

Leleは組み込みプロバイダーと名前付きプロバイダー定義の両方をサポートしています。

現在の設定/ランタイムに含まれる組み込みプロバイダーファミリー:

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

また、以下のようなモデルごとの設定を持つ名前付きOpenAI互換プロバイダーエントリもサポートしています:

- `model`
- `context_window`
- `max_tokens`
- `temperature`
- `vision`
- `reasoning`

## チャネル

ゲートウェイは現在、以下のチャネルの設定を含んでいます:

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

チャネルによってはシンプルなトークンベースの統合である一方、Webhookやブリッジのセットアップを必要とするものもあります。

## ワークスペースレイアウト

デフォルトのワークスペース:

```text
~/.lele/workspace/
```

典型的な内容:

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

このワークスペース中心のレイアウトは、Leleを実用的で効率的なものにしている要素の一つです。状態、プロンプト、スキル、オートメーションが予測可能な場所に配置されています。

## スケジュール、スキル、サブエージェント

### スケジュールタスク

`lele cron`を使用して、一度きりまたは定期的なジョブを作成します。

例:

```bash
lele cron list
lele cron add --name reminder --message "Check backups" --every "2h"
```

### ハートビート

Leleは定期的にワークスペースから`HEARTBEAT.md`を読み取り、タスクを自動的に実行できます。

### スキル

組み込みおよびカスタムスキルは、以下で管理できます:

```bash
lele skills list
lele skills search
lele skills install <skill>
```

### サブエージェント

Leleはサブエージェントによる非同期の委譲作業をサポートしています。長時間実行や並列化可能なタスクに役立ちます。

詳細は`docs/SKILL_SUBAGENTS.md`を参照してください。

## セキュリティモデル

Leleは、エージェントのファイルおよびコマンドアクセスを設定されたワークスペースに制限できます。

主なコントロール:

- `restrict_to_workspace`
- exec拒否パターン
- 機密性の高いアクションへの承認フロー
- ネイティブクライアント向けのトークンベース認証
- ネイティブファイルアップロードのアップロード制限とTTL

運用の詳細については`docs/tools_configuration.md`および`docs/client-api.md`を参照してください。

## CLIリファレンス

| コマンド | 説明 |
| --- | --- |
| `lele onboard` | 設定とワークスペースを初期化 |
| `lele agent` | インタラクティブなエージェントセッションを開始 |
| `lele agent -m "..."` | 一度限りのプロンプトを実行 |
| `lele gateway` | メッセージングゲートウェイを起動 |
| `lele web start` | 組み込みWeb UIを起動 |
| `lele web status` | Web UIのステータスを表示 |
| `lele auth login` | サポートされているプロバイダーで認証 |
| `lele status` | ランタイムのステータスを表示 |
| `lele cron list` | スケジュール済みジョブを一覧表示 |
| `lele cron add ...` | スケジュール済みジョブを追加 |
| `lele skills list` | インストール済みスキルを一覧表示 |
| `lele client pin` | ペアリングPINを生成 |
| `lele client list` | ペアリング済みネイティブクライアントを一覧表示 |
| `lele version` | バージョン情報を表示 |

## 追加ドキュメント

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

## 開発

便利なターゲット:

```bash
make build
make test
make fmt
make vet
make check
```

## ライセンス

MIT
