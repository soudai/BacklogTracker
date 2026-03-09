package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type openAIProvider struct {
	httpClient *http.Client
	endpoint   string
	apiKey     string
	model      string
	maxRetries int
	sleep      func(context.Context, time.Duration) error
}

type openAIRequest struct {
	Model string               `json:"model"`
	Input []openAIInputMessage `json:"input"`
	Text  openAITextConfig     `json:"text"`
}

type openAIInputMessage struct {
	Role    string              `json:"role"`
	Content []openAIContentPart `json:"content"`
}

type openAIContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openAITextConfig struct {
	Format openAIJSONSchemaFormat `json:"format"`
}

type openAIJSONSchemaFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

type openAIResponse struct {
	Output []openAIOutputItem `json:"output"`
}

type openAIOutputItem struct {
	Type    string             `json:"type"`
	Content []openAIOutputPart `json:"content"`
}

type openAIOutputPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (p *openAIProvider) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	return generateWithRetry(ctx, p.maxRetries, p.sleep, func(ctx context.Context) (GenerateResult, error) {
		return p.generateOnce(ctx, req)
	})
}

func (p *openAIProvider) generateOnce(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	schemaJSON := json.RawMessage(req.SchemaJSON)
	if !json.Valid(schemaJSON) {
		return GenerateResult{}, fmt.Errorf("schema JSON is invalid")
	}

	responseBody, err := postJSON(ctx, p.httpClient, "POST", p.endpoint, map[string]string{
		"Authorization": "Bearer " + p.apiKey,
	}, openAIRequest{
		Model: p.model,
		Input: []openAIInputMessage{
			{
				Role: "system",
				Content: []openAIContentPart{
					{Type: "input_text", Text: req.SystemPrompt},
				},
			},
			{
				Role: "user",
				Content: []openAIContentPart{
					{Type: "input_text", Text: req.UserPrompt},
				},
			},
		},
		Text: openAITextConfig{
			Format: openAIJSONSchemaFormat{
				Type:   "json_schema",
				Name:   string(req.Task),
				Schema: schemaJSON,
				Strict: true,
			},
		},
	})
	if err != nil {
		if httpErr, ok := err.(*HTTPStatusError); ok {
			httpErr.Provider = "openai"
		}
		return GenerateResult{}, err
	}

	outputJSON, err := extractOpenAIOutputJSON(responseBody)
	if err != nil {
		return GenerateResult{}, &InvalidOutputError{Provider: "openai", Err: err}
	}
	output, canonicalJSON, err := ValidateStructuredOutput(req.Task, outputJSON)
	if err != nil {
		return GenerateResult{}, &InvalidOutputError{Provider: "openai", Err: err}
	}

	return GenerateResult{
		Output:      output,
		OutputJSON:  canonicalJSON,
		RawResponse: responseBody,
	}, nil
}

func extractOpenAIOutputJSON(responseBody []byte) ([]byte, error) {
	var response openAIResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}

	for _, item := range response.Output {
		for _, part := range item.Content {
			if part.Type == "output_text" && part.Text != "" {
				return []byte(part.Text), nil
			}
		}
	}

	return nil, fmt.Errorf("openai response does not contain output_text")
}

func generateWithRetry(ctx context.Context, maxRetries int, sleep func(context.Context, time.Duration) error, fn func(context.Context) (GenerateResult, error)) (GenerateResult, error) {
	attempts := maxRetries + 1
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt == attempts || !ShouldRetry(err) {
			break
		}
		if err := sleep(ctx, retryDelay(attempt)); err != nil {
			return GenerateResult{}, err
		}
	}

	return GenerateResult{}, lastErr
}
