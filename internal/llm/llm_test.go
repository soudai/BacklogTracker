package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/soudai/BacklogTracker/internal/config"
	"github.com/soudai/BacklogTracker/internal/prompts"
)

func TestOpenAIProviderGeneratePeriodSummary(t *testing.T) {
	t.Parallel()

	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer openai-key" {
			t.Fatalf("Authorization = %q", got)
		}

		if attempts == 1 {
			writeJSON(t, w, map[string]any{
				"output": []map[string]any{
					{
						"type": "message",
						"content": []map[string]any{
							{"type": "output_text", "text": `{"reportType":"period_summary","headline":"x"}`},
						},
					},
				},
			})
			return
		}

		writeJSON(t, w, map[string]any{
			"output": []map[string]any{
				{
					"type": "message",
					"content": []map[string]any{
						{"type": "output_text", "text": `{"reportType":"period_summary","headline":"見出し","overview":"概要","keyPoints":["a"],"riskItems":[],"counts":{"total":1}}`},
					},
				},
			},
		})
	}))
	defer server.Close()

	provider, err := NewFromConfig(config.Config{
		LLMProvider:       config.ProviderChatGPT,
		LLMTimeoutSeconds: 10,
		LLMMaxRetries:     1,
		OpenAIAPIKey:      "openai-key",
		OpenAIModel:       "gpt-test",
	}, WithOpenAIEndpoint(server.URL+"/v1/responses"), WithSleep(func(context.Context, time.Duration) error { return nil }))
	if err != nil {
		t.Fatalf("NewFromConfig returned error: %v", err)
	}

	schemaJSON, err := prompts.OutputSchemaJSON(prompts.TaskPeriodSummary)
	if err != nil {
		t.Fatalf("OutputSchemaJSON returned error: %v", err)
	}

	result, err := provider.Generate(context.Background(), GenerateRequest{
		Task:         prompts.TaskPeriodSummary,
		SystemPrompt: "system",
		UserPrompt:   "user",
		SchemaJSON:   schemaJSON,
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	output, ok := result.Output.(PeriodSummaryOutput)
	if !ok {
		t.Fatalf("result.Output type = %T, want PeriodSummaryOutput", result.Output)
	}
	if output.Counts.Total != 1 {
		t.Fatalf("output.Counts.Total = %d, want 1", output.Counts.Total)
	}
}

func TestGeminiProviderGenerateAccountReport(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/models/gemini-test:generateContent") {
			t.Fatalf("path = %q, want gemini generateContent", r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "gemini-key" {
			t.Fatalf("x-goog-api-key = %q", got)
		}

		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{"text": `{"reportType":"account_report","account":{"id":"yamada","displayName":"山田"},"summary":"summary","issues":[{"issueKey":"PROJ-1","title":"Title","status":"Open","summary":"Issue summary","responseSuggestion":{"message":"Reply","confidence":"high","needsConfirmation":false}}]}`},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	provider, err := NewFromConfig(config.Config{
		LLMProvider:       config.ProviderGemini,
		LLMTimeoutSeconds: 10,
		LLMMaxRetries:     0,
		GeminiAPIKey:      "gemini-key",
		GeminiModel:       "gemini-test",
	}, WithGeminiBaseURL(server.URL))
	if err != nil {
		t.Fatalf("NewFromConfig returned error: %v", err)
	}

	schemaJSON, err := prompts.OutputSchemaJSON(prompts.TaskAccountReport)
	if err != nil {
		t.Fatalf("OutputSchemaJSON returned error: %v", err)
	}

	result, err := provider.Generate(context.Background(), GenerateRequest{
		Task:         prompts.TaskAccountReport,
		SystemPrompt: "system",
		UserPrompt:   "user",
		SchemaJSON:   schemaJSON,
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	output, ok := result.Output.(AccountReportOutput)
	if !ok {
		t.Fatalf("result.Output type = %T, want AccountReportOutput", result.Output)
	}
	if got, want := output.Account.ID, "yamada"; got != want {
		t.Fatalf("output.Account.ID = %q, want %q", got, want)
	}
}

func TestValidateStructuredOutputRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	_, _, err := ValidateStructuredOutput(prompts.TaskPeriodSummary, []byte(`{"reportType":"period_summary","headline":"h","overview":"o","keyPoints":[],"riskItems":[],"counts":{"total":1},"extra":true}`))
	if err == nil {
		t.Fatalf("ValidateStructuredOutput expected error")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ValidateStructuredOutput error = %q, want unknown field", err.Error())
	}
}

func TestSaveRawResponse(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)

	path, err := SaveRawResponse(baseDir, "./data/raw", "job-1", config.ProviderGemini, prompts.TaskPeriodSummary, []byte(`{"ok":true}`), now)
	if err != nil {
		t.Fatalf("SaveRawResponse returned error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw response: %v", err)
	}
	if got, want := string(content), `{"ok":true}`; got != want {
		t.Fatalf("raw response content = %q, want %q", got, want)
	}
	if !strings.Contains(path, filepath.Join("raw", string(config.ProviderGemini), string(prompts.TaskPeriodSummary))) {
		t.Fatalf("path = %q, want provider/task hierarchy", path)
	}
}

func TestStatusCodeAndTemporaryClassification(t *testing.T) {
	t.Parallel()

	err := &HTTPStatusError{Provider: "openai", StatusCode: 429}
	statusCode, ok := StatusCode(err)
	if !ok || statusCode != 429 {
		t.Fatalf("StatusCode = (%d,%t), want (429,true)", statusCode, ok)
	}
	if !IsTemporaryError(err) {
		t.Fatalf("IsTemporaryError expected true")
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}
