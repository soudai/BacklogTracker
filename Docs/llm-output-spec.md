# LLM 出力仕様

## 1. 目的

Gemini と ChatGPT を切り替えても、アプリケーション内部では同一の出力構造で扱えるようにする。自由文の品質だけでなく、機械処理可能な構造を優先する。

## 2. 基本方針

- 出力は JSON を第一とする
- アプリケーション側で JSON Schema 検証を行う
- モデルに渡していない情報は推測で補完させない
- 断定できない内容は `unknown` や `needsConfirmation=true` として返させる
- 出力言語は既定で日本語
- プロンプト本文は Go コードに埋め込まず、外部ファイルとして管理する
- Backlog 本文やコメントは LLM リクエストに含めてもよい
- Slack に出す内容は LLM 出力をそのまま使わず、個人情報マスキングと本文圧縮を通す

## 3. タスク種別

| タスク | 説明 |
| --- | --- |
| `period_summary` | 指定期間の課題群の全体要約 |
| `account_report` | 指定アカウントの課題一覧、課題別要約、回答例提案 |

## 4. 入力データ方針

### 4.1 period_summary

モデルに渡す情報の例:

- 対象期間
- プロジェクト名 / キー
- 課題件数
- 課題ごとの最小情報
  - 課題キー
  - 件名
  - ステータス
  - 優先度
  - 担当者
  - 更新日時
  - 説明の抜粋
  - 直近コメント要点

### 4.2 account_report

モデルに渡す情報の例:

- 対象アカウント
- 課題一覧
- 各課題の最新状況
- 直近コメント
- 回答例作成に必要な会話文脈

## 5. 共通プロンプト制約

- あなたは Backlog の課題分析アシスタントである
- 入力データに含まれる情報だけを使う
- 課題キー、担当者、期限、ステータスを勝手に変更しない
- 回答例はそのまま送信可能な文体に近づけつつ、確認が必要な点を明示する
- 開発者向けの内部説明ではなく、業務利用者が読みやすい形にする

## 5.1 プロンプト管理方針

- プロンプトは `prompts/` 配下のファイルとして保存する
- 代表的なファイル名
  - `prompts/period_summary/system.tmpl`
  - `prompts/period_summary/user.tmpl`
  - `prompts/account_report/system.tmpl`
  - `prompts/account_report/user.tmpl`
- テンプレートには対象期間、プロジェクト情報、課題一覧、出力制約などを変数として渡す
- 実行時に利用したプロンプトファイル名と内容ハッシュを SQLite に保存する
- 調整しやすさのため、アプリ再ビルドなしでファイル差し替えできる構成にする

## 5.2 テンプレート変数の例

### period_summary

- `ProjectName`
- `ProjectKey`
- `DateFrom`
- `DateTo`
- `IssueCount`
- `IssuesJSON`
- `OutputSchemaJSON`

### account_report

- `AccountName`
- `AccountID`
- `DateFrom`
- `DateTo`
- `IssuesJSON`
- `OutputSchemaJSON`

## 6. 出力スキーマ

### 6.1 PeriodSummaryOutput

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": [
    "reportType",
    "headline",
    "overview",
    "keyPoints",
    "riskItems",
    "counts"
  ],
  "properties": {
    "reportType": {
      "type": "string",
      "enum": ["period_summary"]
    },
    "headline": {
      "type": "string"
    },
    "overview": {
      "type": "string"
    },
    "keyPoints": {
      "type": "array",
      "items": { "type": "string" }
    },
    "riskItems": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["issueKey", "reason"],
        "properties": {
          "issueKey": { "type": "string" },
          "reason": { "type": "string" }
        }
      }
    },
    "counts": {
      "type": "object",
      "additionalProperties": false,
      "required": ["total"],
      "properties": {
        "total": { "type": "integer" },
        "open": { "type": ["integer", "null"] },
        "inProgress": { "type": ["integer", "null"] },
        "resolved": { "type": ["integer", "null"] },
        "closed": { "type": ["integer", "null"] }
      }
    }
  }
}
```

### 6.2 AccountReportOutput

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": [
    "reportType",
    "account",
    "summary",
    "issues"
  ],
  "properties": {
    "reportType": {
      "type": "string",
      "enum": ["account_report"]
    },
    "account": {
      "type": "object",
      "additionalProperties": false,
      "required": ["id", "displayName"],
      "properties": {
        "id": { "type": "string" },
        "displayName": { "type": "string" }
      }
    },
    "summary": {
      "type": "string"
    },
    "issues": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": [
          "issueKey",
          "title",
          "status",
          "summary",
          "responseSuggestion"
        ],
        "properties": {
          "issueKey": { "type": "string" },
          "title": { "type": "string" },
          "status": { "type": "string" },
          "summary": { "type": "string" },
          "responseSuggestion": {
            "type": "object",
            "additionalProperties": false,
            "required": ["message", "confidence", "needsConfirmation"],
            "properties": {
              "message": { "type": "string" },
              "confidence": {
                "type": "string",
                "enum": ["high", "medium", "low"]
              },
              "needsConfirmation": { "type": "boolean" }
            }
          }
        }
      }
    }
  }
}
```

## 7. 期待する出力例

### 7.1 period_summary

```json
{
  "reportType": "period_summary",
  "headline": "今週は障害対応の比率が高く、未解決の高優先度課題が残っています。",
  "overview": "対象15件のうち高優先度は4件で、決済と通知機能に関する調査が集中しました。",
  "keyPoints": [
    "決済エラーに関する調査と修正が継続中",
    "通知遅延の再現条件がまだ固まっていない"
  ],
  "riskItems": [
    {
      "issueKey": "PROJ-123",
      "reason": "顧客影響があり、回避策が未確定"
    }
  ],
  "counts": {
    "total": 15,
    "open": 3,
    "inProgress": 7,
    "resolved": 4,
    "closed": 1
  }
}
```

### 7.2 account_report

```json
{
  "reportType": "account_report",
  "account": {
    "id": "yamada",
    "displayName": "山田 太郎"
  },
  "summary": "山田さんは高優先度課題を2件担当しており、1件は顧客回答待ちです。",
  "issues": [
    {
      "issueKey": "PROJ-123",
      "title": "決済画面でエラーが発生する",
      "status": "In Progress",
      "summary": "再現条件は絞れているが、恒久対応は未完了です。",
      "responseSuggestion": {
        "message": "ご報告ありがとうございます。再現条件は確認できており、現在修正内容を確認しています。恒久対応の見込み時刻は別途共有します。",
        "confidence": "medium",
        "needsConfirmation": true
      }
    }
  ]
}
```

## 8. ガードレール

- 回答例に日時や完了見込みを書く場合、入力に存在しないなら確約表現を避ける
- 課題の状態が不明な場合は不明と返す
- コメントが空なら、回答例は「追加確認が必要」と明示する
- 個人情報や秘匿情報が入力に含まれる場合、Slack に出す内容は必要最小限に圧縮する
- Backlog 本文の全文を Slack 通知へそのまま転記しない

## 9. 実装メモ

- OpenAI では Responses API の structured outputs または SDK の parse helper を優先する
- Gemini では `response_mime_type=application/json` と JSON Schema を使う
- どちらのプロバイダでも、アプリ側で最終バリデーションを行う
- スキーマ不一致時は raw response をデバッグ用途として保持しつつ、通常通知には流さない
- プロンプト更新時に出力品質が変わるため、テンプレート単体テストとスナップショット比較を用意する
- 課題本文やコメントをプロンプトに埋め込む前に、個人情報や不要な長文を削減する前処理を入れる
- `--dry-run` ではレンダリング後の system / user prompt を確認し、必要なら `PROMPT_PREVIEW_DIR` に保存する
- `--dry-run` でも Backlog 取得と LLM 呼び出しは行い、投稿系アクションだけ抑止する

## 10. Go での受け取り例

```go
type PeriodSummaryOutput struct {
    ReportType string   `json:"reportType"`
    Headline   string   `json:"headline"`
    Overview   string   `json:"overview"`
    KeyPoints  []string `json:"keyPoints"`
}
```
