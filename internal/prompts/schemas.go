package prompts

const periodSummaryOutputSchema = `{
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
      "required": ["total", "open", "inProgress", "resolved", "closed"],
      "properties": {
        "total": { "type": "integer" },
        "open": { "type": ["integer", "null"] },
        "inProgress": { "type": ["integer", "null"] },
        "resolved": { "type": ["integer", "null"] },
        "closed": { "type": ["integer", "null"] }
      }
    }
  }
}`

const accountReportOutputSchema = `{
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
}`
