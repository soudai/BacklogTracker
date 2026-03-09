# GitHub Issue Drafts

このファイルは、現時点の仕様を実装するために GitHub Issue として起票する想定の下書きです。

## Issue 1

### Title

`Bootstrap CLI and interactive init flow`

### Body

```md
## Summary
Go 製 CLI の土台を作成し、`init` コマンドで対話的に初期設定できるようにする。

## Scope
- Go module 初期化
- `cmd/backlog-tracker` のエントリポイント作成
- 基本的な CLI ルーティング
- `.env` / `.env.local` / 環境変数 / CLI フラグの優先順位実装
- `init` コマンドの対話入力
- `init` で LLM provider を `gemini` / `chatgpt` から必須選択
- 既定 provider を `gemini` に設定
- `.env.local` の生成
- `data/`, `reports/`, `raw/`, `prompt-previews/` の初期作成

## Acceptance Criteria
- `backlog-tracker init` が実行できる
- `init` が `.env.local` を生成する
- provider は `gemini` / `chatgpt` の必須選択で、既定値は `gemini`
- 選択 provider に必要な API key / model が未設定ならエラーになる
- `.env.local` は Git 管理対象外

## References
- Docs/cli-spec.md
- Docs/integration-spec.md
- Docs/requirements.md
```

## Issue 2

### Title

`Implement SQLite migration runner and persistence layer`

### Body

```md
## Summary
SQLite を使った永続化基盤を実装し、SQL ファイル順次適用ベースの migration を導入する。

## Scope
- `migrations/*.sql` の順次適用
- `schema_migrations` 管理
- `job_runs`, `notification_logs`, `prompt_runs` の repository 実装
- migration 適用状況確認の仕組み
- `init` 実行時の migration 起動

## Acceptance Criteria
- `migrations/0001_initial.sql` が適用できる
- 未適用 migration のみ順に実行される
- 実行履歴、通知結果、prompt 実行情報を SQLite に保存できる
- migration エラー時は CLI 終了コードが固定され、テストで担保される

## References
- migrations/0001_initial.sql
- Docs/architecture.md
- Docs/development-plan.md
```

## Issue 3

### Title

`Add Backlog client wrapper and issue collection usecases`

### Body

```md
## Summary
`github.com/kenzo0107/backlog` をラップし、課題取得と補助データ取得の責務を集約する。

## Scope
- Backlog client wrapper 実装
- 認証と base URL 正規化
- `GET /issues` のページネーション
- `GET /issues/:issueIdOrKey/comments`
- `GET /projects/:projectIdOrKey/users`
- status 情報取得
- `init` 用の接続確認
- account-report 向け assignee ベース抽出

## Acceptance Criteria
- 指定期間の課題一覧を全件取得できる
- assignee 指定で課題抽出できる
- コメント一覧を取得できる
- Backlog 認証設定ミスと一時失敗を切り分けられる

## References
- Docs/integration-spec.md
- Docs/requirements.md
```

## Issue 4

### Title

`Implement prompt loader, preview artifacts, and retention policy`

### Body

```md
## Summary
外部 prompt テンプレートの読込・レンダリング・ハッシュ記録・preview 保存を実装する。

## Scope
- `prompts/` の system/user テンプレート読込
- `text/template` ベースの render
- prompt hash 生成
- `PROMPT_PREVIEW_DIR` への preview 保存
- `PROMPT_ARTIFACT_RETENTION_DAYS` の既定値 30 実装
- 古い preview の掃除方針実装
- `--dry-run` で render 結果表示

## Acceptance Criteria
- `period_summary` / `account_report` の prompt を render できる
- render 結果の hash を保存できる
- `--dry-run` で system/user prompt を確認できる
- preview 保持期間は env で変更でき、既定値は 30 日

## References
- Docs/prompt-management.md
- Docs/llm-output-spec.md
```

## Issue 5

### Title

`Add Gemini/OpenAI adapters and structured output validation`

### Body

```md
## Summary
Gemini / ChatGPT のアダプタを実装し、共通 DTO と structured output 検証を行う。

## Scope
- LLM provider interface 実装
- Gemini adapter
- OpenAI adapter
- model / timeout / retry 設定
- JSON schema または構造体ベースの検証
- raw response 保存
- provider 切替

## Acceptance Criteria
- `gemini` と `chatgpt` を CLI から切り替えられる
- 同一 DTO に変換できる
- schema 不一致時の失敗と再試行が実装されている
- `dry-run` でも LLM 呼び出しが実行される

## References
- Docs/integration-spec.md
- Docs/llm-output-spec.md
```

## Issue 6

### Title

`Implement period-summary command with Slack Block Kit notification`

### Body

```md
## Summary
指定期間の課題サマリ機能を実装し、Slack に Block Kit で通知する。

## Scope
- `period-summary` サブコマンド
- Backlog 課題取得
- domain 変換
- prompt render
- LLM 要約生成
- Block Kit payload 生成
- `text` フォールバック生成
- Slack 投稿
- 実行結果を SQLite / ファイルに保存

## Acceptance Criteria
- 指定期間サマリが生成できる
- Slack に Block Kit で通知できる
- `text` フォールバックを含む
- Backlog 本文の生データが Slack に出ない
- `dry-run` では Slack 投稿しない

## References
- Docs/requirements.md
- Docs/cli-spec.md
- Docs/integration-spec.md
```

## Issue 7

### Title

`Implement account-report command for assignee-only issues`

### Body

```md
## Summary
assignee ベースで課題を抽出し、課題要約と回答例を生成して Slack に通知する。

## Scope
- `account-report` サブコマンド
- assignee ベースの課題抽出
- コメント取得
- 回答例生成
- Slack 通知
- SQLite / テキストファイル保存

## Acceptance Criteria
- assignee 指定で課題を抽出できる
- 各課題の要約と回答例が返る
- Slack に通知できる
- 本文の生データと未マスク個人情報が Slack に含まれない

## References
- Docs/requirements.md
- Docs/cli-spec.md
```

## Issue 8

### Title

`Add test coverage for error codes, dry-run semantics, and sanitization`

### Body

```md
## Summary
終了コード、dry-run、副作用抑止、Slack 向けマスキングをテストで固定する。

## Scope
- 設定解決のテスト
- `init` の対話入力テスト
- migration テスト
- prompt render テスト
- provider 切替テスト
- dry-run 時の副作用なしテスト
- Slack payload の Block Kit テスト
- PII masking / 本文除去テスト
- 終了コードの固定化テスト

## Acceptance Criteria
- `go test ./...` で主要ケースを検証できる
- dry-run 時に投稿系アクションが抑止されることを確認できる
- エラー分類と終了コード対応がテストで担保される
- Slack 向け payload に本文の生データや未マスク個人情報が含まれない

## References
- Docs/development-plan.md
- Docs/requirements.md
```
