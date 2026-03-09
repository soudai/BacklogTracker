# BacklogTracker

BacklogTracker は、Backlog の課題情報を取得し、Gemini または ChatGPT で要約や回答案を生成し、その結果を Slack に通知する Go 製 CLI ツールです。

実行環境は Ubuntu を想定しています。永続化には SQLite とテキストファイルを使い、プロンプトはファイルとして外出ししているため、コードを変更せずに調整できます。

## できること

- 指定期間の Backlog 課題を集計し、期間サマリを生成する
- 指定した Backlog アカウントの担当課題を取得し、課題ごとの要約と回答案を生成する
- LLM プロバイダとして `gemini` または `chatgpt` を切り替えて実行する
- 生成結果を Slack に Block Kit 形式で通知する
- 実行履歴、通知履歴、使用プロンプト、生成レポートをローカルに保存する
- `--dry-run` で Backlog / LLM までは実行しつつ、Slack 投稿を抑止して確認する

## 前提環境

- Ubuntu
- Go 1.24.2 以上
- Backlog の API キー
- Gemini または OpenAI の API キー
- Slack Incoming Webhook URL、または Slack Bot Token と通知先チャンネル

SQLite は `modernc.org/sqlite` を利用しているため、別途 SQLite クライアントやライブラリのインストールは必須ではありません。

## インストール

### ソースからビルドする

```bash
git clone https://github.com/soudai/BacklogTracker.git
cd BacklogTracker
go build -o bin/backlog-tracker ./cmd/backlog-tracker
```

ビルド後は次のように実行できます。

```bash
./bin/backlog-tracker help
```

### `go install` を使う

```bash
go install ./cmd/backlog-tracker
```

`$GOBIN` または `$HOME/go/bin` に `backlog-tracker` が配置されます。

## クイックスタート

### 1. 対話的に初期設定する

もっとも簡単なのは `init` を使う方法です。

```bash
./bin/backlog-tracker init
```

`init` では次を対話的に確認します。

- Backlog base URL
- Backlog API key
- Backlog project key
- LLM provider
- 選択したプロバイダに対応する API key / model
- Slack webhook URL または bot token / channel
- SQLite DB パス
- レポート保存先
- raw response 保存先
- prompt preview 保存先
- prompt テンプレート保存先
- prompt / preview 保持日数
- タイムゾーン

`init` は次も行います。

- Backlog 接続確認
- `.env.local` の生成
- 必要ディレクトリの作成
- SQLite migration の適用

### 2. 期間サマリを実行する

```bash
./bin/backlog-tracker period-summary \
  --project PROJ \
  --from 2026-03-01 \
  --to 2026-03-07 \
  --provider gemini
```

### 3. アカウント別レポートを実行する

```bash
./bin/backlog-tracker account-report \
  --project PROJ \
  --account yamada \
  --from 2026-03-01 \
  --to 2026-03-07 \
  --provider chatgpt
```

## 設定ファイル

既定では `.env.local` を使います。共有したい既定値は `.env`、ローカル専用の秘密情報は `.env.local` に置く運用を想定しています。

設定の解決順は次です。

1. CLI フラグ
2. 環境変数
3. `.env.local`
4. `.env`
5. 既定値

`init` の入力補助として [`.env.example`](./.env.example) と [`.env.local.example`](./.env.local.example) も用意しています。

### 最低限必要な設定

Gemini を使う場合:

```dotenv
BACKLOG_BASE_URL=https://your-space.backlog.com
BACKLOG_API_KEY=your-backlog-api-key
BACKLOG_PROJECT_KEY=PROJ

LLM_PROVIDER=gemini
GEMINI_API_KEY=your-gemini-api-key
GEMINI_MODEL=gemini-2.5-pro

SLACK_WEBHOOK_URL=https://hooks.slack.com/services/replace/me
```

ChatGPT を使う場合:

```dotenv
BACKLOG_BASE_URL=https://your-space.backlog.com
BACKLOG_API_KEY=your-backlog-api-key
BACKLOG_PROJECT_KEY=PROJ

LLM_PROVIDER=chatgpt
OPENAI_API_KEY=your-openai-api-key
OPENAI_MODEL=gpt-4.1

SLACK_WEBHOOK_URL=https://hooks.slack.com/services/replace/me
```

Slack Bot Token を使う場合は、`SLACK_BOT_TOKEN` に加えて `SLACK_CHANNEL` が必要です。

### 主な既定値

| 変数 | 既定値 |
| --- | --- |
| `APP_TIMEZONE` | `Asia/Tokyo` |
| `SQLITE_DB_PATH` | `./data/backlog-tracker.sqlite3` |
| `MIGRATION_DIR` | `./migrations` |
| `REPORT_DIR` | `./data/reports` |
| `RAW_RESPONSE_DIR` | `./data/raw` |
| `PROMPT_PREVIEW_DIR` | `./data/prompt-previews` |
| `PROMPT_DIR` | `./prompts` |
| `PROMPT_ARTIFACT_RETENTION_DAYS` | `30` |
| `LLM_PROVIDER` | `gemini` |
| `LLM_TIMEOUT_SECONDS` | `60` |
| `LLM_MAX_RETRIES` | `2` |

## コマンド

### `init`

初回セットアップ用コマンドです。

```bash
./bin/backlog-tracker init
./bin/backlog-tracker init --non-interactive --env-file .env.local --yes --force
./bin/backlog-tracker init --migrate-only --db-path ./data/backlog-tracker.sqlite3
```

主なオプション:

- `--env-file`: 設定ファイルの保存先。既定は `.env.local`
- `--non-interactive`: 対話なしで設定を読み込む
- `--force`: 既存の env ファイルを上書きする
- `--skip-migrate`: env 作成だけを行い migration は実行しない
- `--migrate-only`: migration のみを実行する
- `--db-path`: SQLite DB パスを上書きする
- `--yes`: 確認プロンプトを省略する

### `period-summary`

指定期間の課題サマリを生成します。

```bash
./bin/backlog-tracker period-summary \
  --project PROJ \
  --from 2026-03-01 \
  --to 2026-03-07 \
  --provider gemini \
  --date-field updated \
  --status Open \
  --status "In Progress"
```

主なオプション:

- `--from`, `--to`: 必須。集計期間
- `--date-field`: `updated` または `created`
- `--assignee`: 担当者で絞り込む
- `--status`: ステータスで絞り込む。複数回指定可能
- `--dry-run`: Slack 投稿を行わずに実行する

### `account-report`

指定した Backlog アカウントの担当課題を取得し、課題ごとの要約と回答案を生成します。対象課題は `assignee` ベースで取得します。

```bash
./bin/backlog-tracker account-report \
  --project PROJ \
  --account yamada \
  --from 2026-03-01 \
  --to 2026-03-07 \
  --max-comments 3 \
  --provider chatgpt
```

主なオプション:

- `--account`: 必須。Backlog の `userId`
- `--from`, `--to`: 任意。更新日で対象期間を絞る
- `--max-comments`: 課題ごとに prompt へ含める最新コメント数。`0` は全件
- `--dry-run`: Slack 投稿を行わずに実行する

## `--dry-run` の挙動

`--dry-run` は単なるテンプレート確認ではありません。次を実行します。

- Backlog の参照系 API 呼び出し
- LLM API 呼び出し
- SQLite への実行履歴保存
- prompt preview と raw response と report の保存

次は実行しません。

- Slack 投稿
- そのほかの破壊的変更

実行結果の標準出力には、少なくとも次が含まれます。

```text
job_id: ...
issue_count: ...
preview_path: ...
raw_response_path: ...
report_path: ...
notification: skipped (dry-run)
```

## 保存物

実行すると次の成果物が保存されます。

- SQLite: `SQLITE_DB_PATH`
  - ジョブ実行履歴
  - 通知履歴
  - 使用プロンプト履歴
- レポート: `REPORT_DIR/period_summary/` または `REPORT_DIR/account_report/`
- raw response: `RAW_RESPONSE_DIR/<provider>/<task>/`
- prompt preview: `PROMPT_PREVIEW_DIR/`
- prompt テンプレート: `PROMPT_DIR/period_summary/`, `PROMPT_DIR/account_report/`

prompt preview と関連成果物の保持日数は `PROMPT_ARTIFACT_RETENTION_DAYS` で制御し、既定値は 30 日です。

## プロンプトの調整

プロンプトはコードに埋め込まず、テンプレートファイルとして管理しています。

```text
prompts/
  period_summary/
    system.tmpl
    user.tmpl
  account_report/
    system.tmpl
    user.tmpl
```

`PROMPT_DIR` または `--prompt-dir` で差し替えできます。プロンプト文言や出力トーンを調整したい場合は、まず `--dry-run` で確認してください。

## Slack 通知とセキュリティ

- Slack 通知は Block Kit を使います
- Slack には Backlog の本文やコメントの生データをそのまま流しません
- メールアドレスや電話番号のような個人情報は Slack 出力前にマスクまたは除去します
- LLM には、要約や回答案の生成に必要な範囲で Backlog 本文やコメントを渡します
- Incoming Webhook の URL や API キーがエラーメッセージにそのまま出ないようにマスクしています

## 終了コード

| コード | 意味 |
| --- | --- |
| `0` | 正常終了 |
| `1` | 入力エラー |
| `2` | Backlog API エラー |
| `3` | LLM エラー |
| `4` | Slack 通知エラー |
| `5` | SQLite またはファイル保存エラー |
| `6` | `init` または migration エラー |

## 関連ドキュメント

詳細な設計や仕様は [Docs](./Docs/README.md) を参照してください。

- [CLI 実行仕様](./Docs/cli-spec.md)
- [アーキテクチャ](./Docs/architecture.md)
- [連携仕様](./Docs/integration-spec.md)
- [プロンプト管理仕様](./Docs/prompt-management.md)
