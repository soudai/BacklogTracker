# CLI 実行仕様

## 1. 目的

本システムは Ubuntu 上で CLI コマンドとして実行される。ユースケースごとにサブコマンドを切り替え、Backlog のデータ取得、LLM 実行、Slack 通知、SQLite とテキストファイルへの保存を一連で行う。

## 2. コマンド名

```text
backlog-tracker
```

## 3. サブコマンド

| サブコマンド | 用途 |
| --- | --- |
| `init` | 初期設定を対話的に作成し、SQLite マイグレーションを適用する |
| `period-summary` | 指定期間の課題サマリを作成して通知する |
| `account-report` | 指定アカウントの課題と回答例を作成して通知する |

## 4. レポート系サブコマンドの共通フラグ

| フラグ | 必須 | 説明 |
| --- | --- | --- |
| `--project` | Yes | Backlog のプロジェクトキーまたは ID |
| `--provider` | Yes | `gemini` または `chatgpt` |
| `--timezone` | No | 既定値は `Asia/Tokyo` |
| `--notify` | No | 既定値は `slack` |
| `--dry-run` | No | 参照系 API は実行するが、Slack 投稿や更新系アクションを行わない |
| `--output-dir` | No | テキストファイル保存先の上書き |
| `--db-path` | No | SQLite ファイルの上書き |
| `--prompt-dir` | No | プロンプトテンプレート保存先の上書き |
| `--env-file` | No | 既定は `.env.local` |
| `--verbose` | No | 詳細ログ出力 |

`init` はこの表の必須フラグ対象外で、専用フラグと対話入力を使う。

## 5. `init`

### 5.1 目的

- 初回利用時の設定項目を対話的に収集する
- `.env.local` を生成する
- SQLite DB を初期化し、`migrations/` を適用する
- サンプルプロンプトや必要ディレクトリを用意する

### 5.2 対話で確認する項目

- Backlog のベース URL
- Backlog API Key
- 既定のプロジェクトキー
- 既定の LLM プロバイダ
  - `gemini` または `chatgpt` を必須選択
  - 既定値は `gemini`
- 選択した LLM プロバイダに対応する API Key とモデル名
  - `gemini` 選択時は `GEMINI_API_KEY` / `GEMINI_MODEL` を必須
  - `chatgpt` 選択時は `OPENAI_API_KEY` / `OPENAI_MODEL` を必須
- Slack Webhook URL または Bot Token
- SQLite 保存先
- レポート出力先
- raw response 出力先
- prompt preview 出力先
- プロンプト保存先
- prompt / preview 保持日数
- タイムゾーン

### 5.3 追加フラグ

| フラグ | 必須 | 説明 |
| --- | --- | --- |
| `--non-interactive` | No | 対話なしで既存の環境変数やテンプレート値を使う |
| `--force` | No | 既存ファイルを上書きする |
| `--skip-migrate` | No | `.env.local` 作成のみ行い、migration は実行しない |
| `--migrate-only` | No | 対話を行わず migration のみ実行する |
| `--yes` | No | 確認プロンプトを省略する |

### 5.4 実行例

```bash
backlog-tracker init
backlog-tracker init --non-interactive --env-file .env.local --yes
backlog-tracker init --migrate-only --db-path ./data/backlog-tracker.sqlite3
```

## 6. `period-summary`

### 6.1 追加フラグ

| フラグ | 必須 | 説明 |
| --- | --- | --- |
| `--from` | Yes | 集計開始日。例: `2026-03-01` |
| `--to` | Yes | 集計終了日。例: `2026-03-07` |
| `--date-field` | No | `updated` または `created`。既定値は `updated` |
| `--assignee` | No | 担当者で絞り込む |
| `--status` | No | ステータスで絞り込む。複数回指定可 |

### 6.2 実行例

```bash
backlog-tracker period-summary \
  --project PROJ \
  --from 2026-03-01 \
  --to 2026-03-07 \
  --provider gemini
```

## 7. `account-report`

### 7.1 追加フラグ

| フラグ | 必須 | 説明 |
| --- | --- | --- |
| `--account` | Yes | Backlog の userId または内部識別子 |
| `--from` | No | 取得開始日 |
| `--to` | No | 取得終了日 |
| `--max-comments` | No | 課題ごとに収集する最新コメント数 |

### 7.2 実行例

```bash
backlog-tracker account-report \
  --project PROJ \
  --account yamada \
  --from 2026-03-01 \
  --to 2026-03-07 \
  --provider chatgpt
```

## 8. 設定の優先順位

1. CLI フラグ
2. 環境変数
3. `.env.local`
4. `.env`
5. 既定値

## 9. 終了コード

| 終了コード | 意味 |
| --- | --- |
| `0` | 正常終了 |
| `1` | 入力エラー |
| `2` | Backlog API エラー |
| `3` | LLM エラー |
| `4` | Slack 通知エラー |
| `5` | SQLite またはファイル保存エラー |
| `6` | 初期化または migration エラー |

## 10. 標準出力と保存

- 標準出力にはジョブ ID、対象件数、保存先、通知結果の概要を出す
- 詳細レポートはテキストファイルに保存する
- ジョブ状態と通知結果は SQLite に保存する
- `--dry-run` では Backlog の参照系 API と LLM API は実行する
- `--dry-run` では Slack 投稿や Backlog 更新などの破壊的変更は実行しない
- `--dry-run` ではレンダリング後の system / user prompt を標準出力または `PROMPT_PREVIEW_DIR` に保存して確認できるようにする

## 11. Ubuntu 運用例

### 11.1 初回初期化

```bash
backlog-tracker init
```

### 11.2 Cron

```cron
0 9 * * 1 /opt/backlog-tracker/backlog-tracker period-summary --project PROJ --from 2026-03-03 --to 2026-03-09 --provider gemini
```

### 11.3 systemd timer

- バイナリ配置例: `/opt/backlog-tracker/backlog-tracker`
- データ配置例: `/var/lib/backlog-tracker/`
- ログ配置例: `/var/log/backlog-tracker/`

## 12. 備考

- `--dry-run` はプロンプト調整や Slack 疎通前の確認に使う
- `init` は `.env.example` と `.env.local.example` を入力補助として利用し、最終的に `.env.local` を作成する
- Slack 通知は Block Kit を使い、`text` はフォールバック用に必ず含める
- Slack Incoming Webhook 利用時は通知先チャンネルが Webhook 側設定に依存する
- Bot Token 利用時のみ将来的に `--slack-channel` の追加を検討する
