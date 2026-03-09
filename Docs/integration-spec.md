# 外部連携仕様

## 1. 連携対象一覧

| サービス | 用途 | 認証 | 推奨 |
| --- | --- | --- | --- |
| Backlog | 課題・コメント・ユーザー情報取得 | API Key または OAuth 2.0 | Go ライブラリ `github.com/kenzo0107/backlog` をラップして利用 |
| OpenAI | ChatGPT 系モデルで要約・回答例生成 | API Key | Responses API を利用 |
| Gemini | Gemini 系モデルで要約・回答例生成 | API Key | Structured Outputs を利用 |
| Slack | 結果通知 | Incoming Webhook または Bot Token | Block Kit を使って通知する |

## 2. Backlog

### 2.1 認証

- 公式ドキュメント
  - https://developer.nulab.com/ja/docs/backlog/auth/
- 内部向け単一テナント運用であれば API Key を第一候補とする
- 将来、利用者ごとの権限分離が必要なら OAuth 2.0 に切り替える

### 2.2 Go ライブラリ

- 利用ライブラリ
  - https://github.com/kenzo0107/backlog
- README では `backlog.New("YOUR API KEY", "YOUR BASE URL")` の形でクライアント生成する例が示されている
- 本プロジェクトではこのライブラリを直接アプリ全体に散らさず、`backlog-client` パッケージ内でラップして使う
- ライブラリが吸収しないページネーション、リトライ、フィルタ組み立て、ドメイン変換はアプリ側で責務を持つ
- `go.mod` では明示的にバージョン固定し、ライブラリ更新はアプリ側ラッパーのテスト通過を条件に行う

### 2.3 ベース URL

- Backlog REST API の基準 URL は `https://{space}.backlog.com/api/v2`
- ただし Go ライブラリに渡す `endpoint` は README の `YOUR BASE URL` に従うため、アプリの設定値は `https://{space}.backlog.com` のようなスペースルート URL で持ち、`backlog-client` 側で必要な形式へ正規化する
- 一部環境では `backlog.jp` または `backlogtool.com` の場合があるため、ホストは設定値で持つ

### 2.4 使用候補 API

| 用途 | メソッド / パス | 備考 |
| --- | --- | --- |
| プロジェクト一覧 | `GET /projects` | 初期確認用 |
| プロジェクトユーザー一覧 | `GET /projects/:projectIdOrKey/users` | 対象アカウント解決 |
| 課題一覧取得 | `GET /issues` | 期間フィルタ、担当者フィルタに対応 |
| コメント一覧取得 | `GET /issues/:issueIdOrKey/comments` | 回答例の文脈取得 |
| プロジェクトステータス一覧 | `GET /projects/:projectIdOrKey/statuses` | 表示名解決 |
| プロジェクトチーム一覧 | `GET /projects/:projectIdOrKey/teams` | 将来のチーム単位配信用 |

### 2.5 注意点

- Backlog の旧 `GET /statuses` は deprecated 扱いで、公式ドキュメントでは 2025-08-28 以降の段階的廃止が案内されている
- 公式 changelog では `2.113.3`（2025-09-11）で旧 Status / Group 系 API の削除が記録されているため、新規実装では利用しない
- 同様にグループ系 API ではなくチーム系 API を優先する
- レート制限はプランとリクエスト種別で異なるため、実装時は 429 を前提にする
- 公式ドキュメント
  - API 概要: https://developer.nulab.com/ja/docs/backlog/
  - 課題一覧: https://developer.nulab.com/docs/backlog/api/2/get-issue-list
  - コメント一覧: https://developer.nulab.com/docs/backlog/api/2/get-comment-list/
  - プロジェクトユーザー一覧: https://developer.nulab.com/docs/backlog/api/2/get-project-user-list/
  - プロジェクトステータス一覧: https://developer.nulab.com/docs/backlog/api/2/get-status-list-of-project/
  - プロジェクトチーム一覧: https://developer.nulab.com/docs/backlog/api/2/get-project-team-list/
  - レート制限: https://developer.nulab.com/docs/backlog/rate-limit/
  - Changelog: https://developer.nulab.com/docs/backlog/changelog/

### 2.6 推奨取得条件

- 期間サマリ
  - `updatedSince`, `updatedUntil` を基本にする
  - 必要に応じて `createdSince`, `createdUntil` に切り替え可能にする
- アカウント別
  - `assigneeId[]` を基本とする
- 共通
  - `count=100` を基本とし、`offset` でページネーション
  - `order=asc` で取得後、アプリ側で整形してもよい

## 3. OpenAI

### 3.1 認証

- 公式ドキュメント
  - https://platform.openai.com/docs/api-reference/introduction
- API キーを `Authorization: Bearer` で送る

### 3.2 利用方針

- 新規実装では Responses API を優先する
- モデル名は固定値を埋め込まず、設定ファイルまたは環境変数から渡す
- 可能な限り structured output を使い、JSON パース前提で扱う

### 3.3 参考ドキュメント

- Text generation: https://developers.openai.com/api/docs/guides/text
- Responses API: https://developers.openai.com/api/reference/responses/create
- Structured Outputs: https://developers.openai.com/api/docs/guides/structured-outputs

### 3.4 実装上の注意

- 出力揺れを避けるため、自由文だけではなく JSON スキーマを要求する
- プロバイダ差分を減らすため、最終的なアプリ内 DTO は Gemini と共通にする
- トークンコストと遅延を抑えるため、課題本文とコメントは必要最小限に整形して送る

## 4. Gemini

### 4.1 認証

- 公式ドキュメント
  - https://ai.google.dev/gemini-api/docs
- API キーを使う方式を基本とする

### 4.2 利用方針

- `generateContent` 系 API を利用する
- JSON スキーマを使った structured output を前提にする
- モデル名は設定可能にする

### 4.3 参考ドキュメント

- テキスト生成: https://ai.google.dev/gemini-api/docs/text-generation
- Structured Outputs: https://ai.google.dev/gemini-api/docs/structured-output
- Rate limits: https://ai.google.dev/gemini-api/docs/rate-limits

### 4.4 実装上の注意

- Structured output は構文保証が中心で、意味的妥当性はアプリ側で検証が必要
- モデルや利用ティアによりレート制限が変わるため、429 と quota 超過を考慮する
- 長文コメントを丸ごと送らず、件数制限と要約前整形を入れる

## 5. Slack

### 5.1 通知方針

- Slack 通知は Block Kit を基本とする
- `text` はアクセシビリティとフォールバックのため必須にする
- MVP は Incoming Webhook を推奨する
- 理由
  - 導入が簡単
  - 通知専用なら十分
  - Block Kit も利用できる

### 5.2 高度な要件が出た場合

次の要件が出たら `chat.postMessage` へ移行する。

- チャンネルを動的に切り替えたい
- スレッド返信を安定して行いたい
- 投稿更新や削除を行いたい
- Bot 権限でチャネル探索したい

### 5.3 参考ドキュメント

- Incoming Webhooks: https://api.slack.com/messaging/webhooks
- `chat.postMessage`: https://api.slack.com/methods/chat.postMessage
- Block Kit: https://api.slack.com/block-kit

### 5.4 メッセージ設計方針

- `text` を必須扱いにする
- `blocks` を基本表現とする
- 1 通知を長くし過ぎず、要点と詳細リンクを分ける
- 課題件数が多い場合はトップ N 件に絞り、残件数を明記する
- Backlog 本文の全文は Slack に送らない
- 個人情報はマスキングまたはカットしてから Slack に渡す

## 6. 推奨環境変数

```env
APP_ENV=local
APP_TIMEZONE=Asia/Tokyo
APP_DATA_DIR=/var/lib/backlog-tracker
SQLITE_DB_PATH=/var/lib/backlog-tracker/backlog-tracker.sqlite3
MIGRATION_DIR=./migrations
REPORT_DIR=/var/lib/backlog-tracker/reports
RAW_RESPONSE_DIR=/var/lib/backlog-tracker/raw
PROMPT_PREVIEW_DIR=/var/lib/backlog-tracker/prompt-previews
PROMPT_ARTIFACT_RETENTION_DAYS=30
PROMPT_DIR=./prompts

BACKLOG_BASE_URL=https://example.backlog.com
BACKLOG_API_KEY=xxxxx
BACKLOG_PROJECT_KEY=PROJ

LLM_PROVIDER=gemini
OPENAI_API_KEY=xxxxx
OPENAI_MODEL=your-openai-model
GEMINI_API_KEY=xxxxx
GEMINI_MODEL=your-gemini-model

SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
SLACK_BOT_TOKEN=
SLACK_CHANNEL=
```

## 6.1 設定の読み込み優先順位

1. CLI フラグ
2. 環境変数
3. `.env.local`
4. `.env`
5. 既定値

- `.env` はローカル開発用とし、起動時に存在すれば読む
- `.env.local` は `init` によって生成するローカル上書き設定とし、存在すれば `.env` より優先する
- `init` では LLM プロバイダを必須選択し、既定値は `gemini` とする
- 本番では原則として OS 環境変数または Secret Manager 由来の環境変数を使う
- 起動時に必須設定を検証し、不足があれば即時終了する

## 6.2 Ubuntu 上の保存先例

- 開発環境
  - `./data/backlog-tracker.sqlite3`
  - `./data/reports/`
  - `./data/raw/`
  - `./data/prompt-previews/`
  - `./prompts/`
  - `./migrations/`
- 運用環境
  - `/var/lib/backlog-tracker/backlog-tracker.sqlite3`
  - `/var/lib/backlog-tracker/reports/`
  - `/var/lib/backlog-tracker/raw/`
  - `/var/lib/backlog-tracker/prompt-previews/`
  - `/var/log/backlog-tracker/`
  - `/opt/backlog-tracker/prompts/`
  - `/opt/backlog-tracker/migrations/`

## 7. エラーハンドリング要件

- Backlog 401/403: 認証設定ミスとして即時失敗
- Backlog 429: バックオフ再試行
- LLM 400: スキーマやリクエストの組み立て不備として調査対象
- LLM 429/5xx: 再試行対象
- Slack 4xx: Webhook URL または権限設定の見直し
- Slack 5xx: 再試行対象
- CLI の終了コードとエラー分類はテストコードで担保する

## 8. シークレット管理

- `.env` はローカル開発専用とする
- `.env.example` をリポジトリに置き、必要なキー名だけ共有する
- `.env.local.example` を `init` の対話入力補助テンプレートとして置く
- 本番は CI/CD または実行環境の Secret Manager を利用する
- ログ、例外、Slack 通知本文に API キーやトークンを埋め込まない
- `.env` 自体は Git 管理対象に含めない
- `.env.local` も Git 管理対象に含めない

## 9. Ubuntu 実行前提

- 配布物は単一の Go バイナリを基本とする
- 手動実行、Cron、systemd timer のいずれからでも同じ CLI を起動できるようにする
- SQLite とレポート出力ディレクトリへの書き込み権限を事前に付与する
- 初回は `backlog-tracker init` を実行し、`.env.local` 作成と SQLite 初期化を済ませる
- SQLite 更新は `.sql` ファイルの順次取り込みだけで運用する
