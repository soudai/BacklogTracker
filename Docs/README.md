# BacklogTracker Docs

このディレクトリは、Backlog の課題データを取得し、Gemini または ChatGPT で要約・回答例生成を行い、その結果を Slack に通知する Go 製 CLI ツールの開発ドキュメントをまとめたものです。

現時点では実装より先に設計を固める段階のため、本ディレクトリは「実装ガイドを兼ねた初期設計書」として扱います。

## ドキュメント一覧

| ファイル | 目的 |
| --- | --- |
| `requirements.md` | 機能要件、非機能要件、受け入れ条件を整理する |
| `architecture.md` | コンポーネント分割、処理フロー、責務境界を定義する |
| `integration-spec.md` | Backlog / OpenAI / Gemini / Slack の接続仕様を整理する |
| `llm-output-spec.md` | 要約と回答例提案のプロンプト方針と出力スキーマを定義する |
| `prompt-management.md` | LLM プロンプトをファイルとして管理・調整する方針を定義する |
| `cli-spec.md` | Ubuntu 上で実行する CLI コマンドの仕様を定義する |
| `development-plan.md` | 推奨ディレクトリ構成、実装ステップ、テスト方針を定義する |

## 想定する提供機能

1. 指定期間のチケットサマリを作成する
2. 指定アカウントに紐づくチケット一覧、各チケットのサマリ、回答例を提案する
3. 実行時に LLM プロバイダとして Gemini または ChatGPT を選択できる
4. 結果を Slack に通知する

## 推奨読了順

1. `requirements.md`
2. `architecture.md`
3. `integration-spec.md`
4. `llm-output-spec.md`
5. `prompt-management.md`
6. `cli-spec.md`
7. `development-plan.md`

## 前提

- 本プロジェクトは単一の Backlog スペースを対象にした内部利用ツールを第一候補とする
- 実装言語は Go とし、Backlog API 連携には `github.com/kenzo0107/backlog` を利用する
- 通知先は Slack のみを対象とし、他チャットツール対応はスコープ外とする
- Backlog への書き戻しは行わず、要約と回答例の提示に留める
- 実行環境は Ubuntu で、ユースケースごとに CLI コマンドとして起動する
- 初回セットアップは `init` コマンドで対話的に行い、`.env.local` 生成と SQLite 初期化をまとめて実施できるようにする
- 構造化データは SQLite、生成成果物やデバッグ用出力はテキストファイルで永続化する
- LLM に渡すプロンプトはファイルとして外出しし、コード変更なしで調整できるようにする
- API Key や接続設定は `.env` と環境変数の両方から読み込めるようにする
- LLM 出力はそのまま信頼せず、アプリケーション側でスキーマ検証して利用する
