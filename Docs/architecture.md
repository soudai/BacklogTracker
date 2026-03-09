# アーキテクチャ設計

## 1. 全体方針

初期実装では単純さと保守性を優先し、以下の分離を行う。

- Ubuntu CLI エントリポイント
- Backlog 取得
- ドメイン整形
- 初期化ウィザード
- プロンプト読み込みとレンダリング
- LLM 生成
- Slack 通知
- ジョブ実行と監視
- SQLite 永続化
- テキストファイル出力

実装言語は Go とし、Backlog API 連携には `github.com/kenzo0107/backlog` を利用する。実行形態は Ubuntu 上の CLI コマンドを前提とする。

## 2. 推奨コンポーネント

| コンポーネント | 責務 |
| --- | --- |
| `cli` | サブコマンド定義、フラグ解析、終了コード制御 |
| `initializer` | `init` の対話入力、`.env.local` 生成、サンプルコピー、ディレクトリ初期化 |
| `job-runner` | ジョブ起動、入力検証、タイムゾーン解決、ジョブ ID 発行 |
| `config` | 環境変数、シークレット、モデル設定の読み込み |
| `backlog-client` | `github.com/kenzo0107/backlog` をラップし、Backlog API 呼び出し、認証、ページネーション、レート制御を担う |
| `issue-collector` | 課題、コメント、ユーザー、ステータス情報の収集 |
| `domain-builder` | API レスポンスを要約用の中間データへ正規化し、LLM 用の完全版と Slack 用のサニタイズ版を分離する |
| `prompt-loader` | `prompts/` 配下のテンプレート読込、変数展開、プロンプトバージョン識別 |
| `llm-provider` | Gemini / ChatGPT を共通インターフェースで呼び出す |
| `migration-runner` | `migrations/` 配下の SQL ファイルを順番に取り込んで適用する |
| `summary-service` | 期間サマリ生成 |
| `account-report-service` | アカウント別サマリと回答例生成 |
| `notification-service` | Slack 送信、再送、結果記録 |
| `job-history` | 実行履歴、重複実行防止、監査用メタデータ保存 |
| `sqlite-store` | ジョブ履歴、通知結果、状態管理を SQLite に保存 |
| `file-store` | レポート、raw response、エラーダンプをテキストファイルに保存 |
| `logger` | 構造化ログと相関 ID の出力 |

## 3. 推奨処理フロー

### 3.1 指定期間の課題サマリ

1. Ubuntu 上で `backlog-tracker period-summary ...` を実行する
2. `cli` が入力を検証し `job-runner` に渡す
3. `job-runner` が `jobId` を採番する
4. `issue-collector` が Backlog から対象課題を取得する
5. `domain-builder` が課題一覧を LLM 入力用に整形し、Slack 用にサニタイズする
6. `prompt-loader` が `period-summary` 用プロンプトファイルを読み込み、入力データでレンダリングする
7. `summary-service` が選択された `llm-provider` を呼ぶ
8. `file-store` に Markdown またはテキスト形式のレポートを保存する
9. `sqlite-store` にジョブ実行結果とプロンプト識別情報を保存する
10. `notification-service` が Slack に送信する
11. `sqlite-store` に通知状態を反映する

### 3.2 指定アカウントの課題 + 回答例

1. Ubuntu 上で `backlog-tracker account-report ...` を実行する
2. `cli` が入力を検証し `job-runner` に渡す
3. `job-runner` が対象アカウントを解決する
4. `issue-collector` が担当課題や関連課題を取得する
5. 各課題について本文、説明、直近コメントを収集する
6. `prompt-loader` が `account-report` 用プロンプトファイルを読み込み、入力データでレンダリングする
7. `account-report-service` が課題単位サマリと回答例を生成する
8. スキーマ検証後、Slack 向けに整形する
9. `file-store` にレポートと回答例を保存する
10. `sqlite-store` にジョブ実行結果とプロンプト識別情報を保存する
11. `notification-service` が Slack に送信する
12. `sqlite-store` に通知状態を反映する

### 3.3 初期セットアップ

1. Ubuntu 上で `backlog-tracker init` を実行する
2. `initializer` が対話的に設定値を収集する
3. `initializer` が `gemini` または `chatgpt` を必須選択させ、既定値に `gemini` を提示する
4. `.env.example` と `.env.local.example` を基に `.env.local` を生成する
5. `initializer` が選択したプロバイダに必要な API Key とモデルが埋まっていることを確認する
6. `initializer` が `data/`、`reports/`、`raw/`、`prompt-previews/` などの必要ディレクトリを作成する
7. `initializer` が `prompts/` のサンプルを配置または確認する
8. `migration-runner` が SQLite DB を作成し、`migrations/` を適用する
9. 完了時に設定ファイル保存先、DB パス、次の実行例を標準出力へ表示する

## 4. 論理構成

```text
Ubuntu Shell / Cron / systemd timer
              |
              v
            CLI
              |
   +----------+----------+
   |                     |
   v                     v
Initializer          Job Runner
   |                     |
   v                     v
Migration Runner   +-----+------+
                   |     |      |
                   v     v      v
                Config Logger Job History
                   |
                   v
             Issue Collector
                   |
                   v
             Domain Builder
                   |
                   v
              Prompt Loader
                   |
                   v
          LLM Provider Adapter
           |               |
           v               v
        Gemini         ChatGPT
                   |
         +---------+---------+
         |                   |
         v                   v
     File Store         SQLite Store
         |                   |
         +---------+---------+
                   |
                   v
        Notification Service
                   |
                   v
                 Slack
                   |
                   v
             SQLite Store
```

## 5. 実行形態

### 5.1 サブコマンド

- `period-summary`
- `account-report`
- `init`

### 5.2 共通実行方針

- CLI は Ubuntu 上で手動実行、Cron、systemd timer のいずれでも起動できるようにする
- 標準出力には実行結果の要約を出し、詳細はログと保存ファイルで追えるようにする
- 異常終了時は非 0 の終了コードを返す
- `--dry-run` では Backlog の参照系 API と LLM API は実行するが、Slack 投稿や更新系アクションは実行しない

## 6. ドメインモデル

### 6.1 JobRequest

```json
{
  "jobType": "period_summary",
  "provider": "gemini",
  "projectKey": "PROJ",
  "dateFrom": "2026-03-01",
  "dateTo": "2026-03-07",
  "dateField": "updated",
  "timezone": "Asia/Tokyo"
}
```

### 6.2 IssueSnapshot

```json
{
  "issueKey": "PROJ-123",
  "summary": "決済画面でエラーが発生する",
  "status": "In Progress",
  "priority": "High",
  "assignee": "suzuki",
  "updated": "2026-03-07T10:15:00+09:00",
  "commentCount": 4,
  "latestComments": [
    {
      "author": "yamada",
      "created": "2026-03-07T09:00:00+09:00",
      "content": "再現条件を確認しました。"
    }
  ]
}
```

### 6.3 SummaryReport

```json
{
  "reportType": "period_summary",
  "headline": "今週は障害対応が中心で、高優先度課題が3件残っています。",
  "keyPoints": [
    "決済関連の障害対応が集中",
    "期限超過の課題が2件ある"
  ],
  "riskItems": [
    {
      "issueKey": "PROJ-123",
      "reason": "顧客影響あり"
    }
  ]
}
```

## 7. Go パッケージ案

```text
cmd/backlog-tracker/main.go
internal/cli
internal/config
internal/domain
internal/initconfig
internal/usecase
internal/backlogclient
internal/prompts
internal/llm
internal/notifications/slack
internal/migrations
internal/storage/sqlite
internal/storage/files
internal/logging
migrations/
prompts/
```

## 8. インターフェース設計

### 8.1 LLM Provider Interface

```go
type LLMProvider interface {
    GeneratePeriodSummary(ctx context.Context, input PeriodSummaryInput) (PeriodSummaryOutput, error)
    GenerateAccountReport(ctx context.Context, input AccountReportInput) (AccountReportOutput, error)
}
```

### 8.2 Notification Interface

```go
type Notifier interface {
    Send(ctx context.Context, message NotificationMessage) error
}
```

### 8.3 Persistence Interface

```go
type JobRepository interface {
    SaveRun(ctx context.Context, run JobRun) error
    UpdateRunStatus(ctx context.Context, jobID string, status string) error
}
```

### 8.4 Prompt Interface

```go
type PromptLoader interface {
    Load(ctx context.Context, name string) (PromptTemplate, error)
    Render(t PromptTemplate, vars any) (string, error)
}
```

### 8.5 Migration Interface

```go
type MigrationRunner interface {
    Migrate(ctx context.Context, dbPath string) error
}
```

## 9. 例外処理方針

- Backlog API の一時失敗は指数バックオフ付きで再試行する
- 4xx は原則として設定ミス扱いとし、即時失敗とする
- LLM 出力のスキーマ不一致時は 1 回だけリトライし、それでも失敗する場合は生テキストを保存してジョブ失敗にする
- Slack 送信失敗時は再送を行い、最終失敗を実行履歴に残す
- CLI は原因に応じて終了コードを分けられるようにする

## 10. 冪等性と監査

- 1 ジョブに対して `jobId` を発行する
- `jobType + project + account + date range + provider` から重複キーを作成する
- 同一重複キーで一定時間内に再実行された場合は、通知を抑止するか上書き戦略を選べるようにする
- 入力条件、取得件数、使用モデル、送信結果、利用したプロンプトファイル名とハッシュを履歴として残す

## 11. 永続化

### 11.1 SQLite に保存するもの

- ジョブメタデータ
- 実行開始・終了時刻
- 通知結果
- エラー理由
- 冪等性キー
- 使用したプロンプト名とハッシュ

### 11.2 テキストファイルに保存するもの

- Slack 送信用の整形済みレポート
- LLM の最終出力
- 調査用の raw response
- 障害時のエラーダンプ

### 11.3 保存先例

- `/var/lib/backlog-tracker/backlog-tracker.sqlite3`
- `/var/lib/backlog-tracker/reports/`
- `/var/lib/backlog-tracker/raw/`
- `/var/lib/backlog-tracker/prompt-previews/`

## 12. 将来拡張

- Backlog Webhook と組み合わせたイベント駆動実行
- Slack スレッド返信や再通知
- ユーザー別ダイジェストの定期配信
- 回答例のトーン切り替え
- 通知チャネルの追加
