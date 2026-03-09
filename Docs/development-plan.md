# 開発計画

## 1. 開発前提

実装が未着手に近いため、まずは最短で MVP を成立させ、その後に保守性を高める順で進める。

推奨技術スタック:

- Go
- `github.com/kenzo0107/backlog`
- 任意の HTTP クライアントまたは標準 `net/http`
- JSON Schema バリデータまたは Go の構造体バリデーション
- SQLite
- テキストファイル保存

## 2. 推奨ディレクトリ構成

```text
cmd/
  backlog-tracker/
internal/
  cli/
  config/
  domain/
  initconfig/
  usecase/
  backlogclient/
  prompts/
  llm/
  notifications/
  storage/
    sqlite/
    files/
  logging/
migrations/
prompts/
testdata/
scripts/
Docs/
```

## 3. 実装ステップ

### Step 1: 設定と土台

- Go module 初期化
- CLI エントリポイント作成
- `init` サブコマンド作成
- 環境変数ローダー
- `.env` ローダーと設定バリデーション
- `.env.local` ローダーと生成ロジック
- `.env.example` の用意
- `.env.local.example` の用意
- 構造化ログ
- ジョブ引数 DTO
- 共通エラーハンドリング
- migration runner の土台作成
- `init` のプロバイダ必須選択と既定値 `gemini` の実装

### Step 2: Backlog クライアント

- `github.com/kenzo0107/backlog` のラッパー実装
- 認証
- `GET /issues` のページネーション
- `GET /issues/:issueIdOrKey/comments`
- `GET /projects/:projectIdOrKey/users`
- レート制限対応
- `init` から使う接続確認用の最小ヘルスチェック実装

### Step 3: 指定期間サマリ

- `period-summary` サブコマンド
- プロンプトテンプレート読込基盤
- 課題一覧の取得
- 中間データ整形
- LLM プロバイダ共通インターフェース
- Slack Block Kit 通知
- SQLite / テキストファイル保存
- `--dry-run` での prompt preview 保存
- Slack 向けマスキング / 本文圧縮

### Step 4: 指定アカウントレポート

- `account-report` サブコマンド
- アカウント解決
- assignee ベースの課題抽出
- 直近コメント取得
- 回答例生成
- Slack 通知と SQLite / テキストファイル保存

### Step 5: 安定運用

- ジョブ履歴
- 冪等制御
- リトライとメトリクス
- 異常系テスト
- Ubuntu での運用スクリプト整理
- プロンプト変更時のレビュー手順とスナップショット更新手順
- `init` 再実行時の上書き / 差分適用戦略整理

## 4. テスト戦略

### 4.1 単体テスト

- Backlog API レスポンスからドメイン変換できること
- `init` の対話入力から `.env.local` を生成できること
- プロンプトテンプレートが正しくレンダリングできること
- LLM 出力をスキーマ検証できること
- Slack メッセージ整形結果が期待通りであること

### 4.2 結合テスト

- Backlog モックレスポンスを使ってジョブが完走すること
- Gemini / OpenAI のアダプタ層で共通 DTO に変換できること
- 通知失敗時に再送ロジックが動くこと
- SQLite とファイル出力が期待通り行われること
- `.env` と環境変数の優先順位どおりに設定解決できること
- migration が未適用分だけ順に走ること
- `dry-run` で参照 API は実行され、投稿系アクションは抑止されること
- Slack 出力に本文の生データと未マスク個人情報が含まれないこと

### 4.3 E2E 相当

- ステージング用 Backlog プロジェクトでサンプル課題を読み取れること
- ステージング Slack チャンネルへ投稿できること
- Ubuntu 上で CLI 実行から保存まで通ること
- `init` 後にすぐ `period-summary --dry-run` を実行できること

## 5. 開発時の注意

- LLM API を直接叩くテストはコストがかかるため、通常は fixture ベースで進める
- 課題本文やコメントの実データを `testdata` に入れる場合、機密情報をマスクする
- Slack 通知は開発用チャンネルに限定する
- `go test ./...`、`go fmt ./...`、`go vet ./...` を基本セットにする
- プロンプト変更は Go コード変更と分けてレビューできる状態を保つ
- `.env.local` は Git 管理対象から除外し、`init` で再生成可能にする
- 終了コードとエラー分類はテストで固定する

## 6. 運用設計の初期案

- 実行方式
  - 手動 CLI 実行
  - Cron からの定期実行
  - systemd timer からの定期実行
- ログ
  - `jobId`
  - `provider`
  - `projectKey`
  - `targetAccount`
  - `issueCount`
  - `notificationStatus`

### 6.1 Ubuntu 実行例

```bash
./backlog-tracker period-summary --project PROJ --from 2026-03-01 --to 2026-03-07 --provider gemini
./backlog-tracker account-report --project PROJ --account yamada --from 2026-03-01 --to 2026-03-07 --provider chatgpt
```

## 7. Definition of Done

- Ubuntu 上で CLI として実行できる
- 指定期間サマリが Slack に送れる
- 指定アカウントレポートが Slack に送れる
- Gemini / ChatGPT の切り替えが動作する
- SQLite にジョブ履歴が保存される
- テキストファイルに生成結果が保存される
- 主要処理に unit test がある
- 失敗時のログで原因箇所を特定できる

## 8. 初回実装の優先順位

1. `init` と SQLite migration を入れる
2. `period-summary` を完成させる
3. SQLite / テキストファイル保存を入れる
4. プロンプト外出しと `.env` / 環境変数設定を入れる
5. Slack 通知を安定させる
6. LLM プロバイダ切り替えを入れる
7. `account-report` を追加する

## 9. 追加提案

- 起動時に設定値を一括検証する `config validate` 相当の処理を入れる
- SQLite migration は SQL ファイル順次適用に留め、適用状況確認だけを追加する
- Cron 多重起動対策として lock file または DB ロックを入れる
- Slack 送信前のマスキング処理を明示的なモジュールとして切り出す
- `init` 時に Backlog / Slack の疎通確認を任意で実行できるようにする
- prompt preview の保存期限や掃除方針を決める
