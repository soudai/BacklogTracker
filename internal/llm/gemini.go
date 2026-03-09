package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type geminiProvider struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	maxRetries int
	sleep      func(context.Context, time.Duration) error
}

type geminiRequest struct {
	SystemInstruction geminiContent          `json:"systemInstruction,omitempty"`
	Contents          []geminiContent        `json:"contents"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	ResponseMIMEType   string          `json:"responseMimeType"`
	ResponseJSONSchema json.RawMessage `json:"responseJsonSchema"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

func (p *geminiProvider) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	return generateWithRetry(ctx, p.maxRetries, p.sleep, func(ctx context.Context) (GenerateResult, error) {
		return p.generateOnce(ctx, req)
	})
}

func (p *geminiProvider) generateOnce(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	schemaJSON := json.RawMessage(req.SchemaJSON)
	if !json.Valid(schemaJSON) {
		return GenerateResult{}, fmt.Errorf("schema JSON is invalid")
	}

	endpoint, err := url.Parse(strings.TrimSuffix(p.baseURL, "/"))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("parse gemini base URL: %w", err)
	}
	endpoint.Path += "/models/" + p.model + ":generateContent"
	endpoint.RawQuery = ""

	responseBody, err := postJSON(ctx, p.httpClient, "POST", endpoint.String(), map[string]string{
		"x-goog-api-key": p.apiKey,
	}, geminiRequest{
		SystemInstruction: geminiContent{
			Parts: []geminiPart{{Text: req.SystemPrompt}},
		},
		Contents: []geminiContent{
			{
				Role:  "user",
				Parts: []geminiPart{{Text: req.UserPrompt}},
			},
		},
		GenerationConfig: geminiGenerationConfig{
			ResponseMIMEType:   "application/json",
			ResponseJSONSchema: schemaJSON,
		},
	})
	if err != nil {
		if httpErr, ok := err.(*HTTPStatusError); ok {
			httpErr.Provider = "gemini"
		}
		return GenerateResult{}, err
	}

	outputJSON, err := extractGeminiOutputJSON(responseBody)
	if err != nil {
		return GenerateResult{}, &InvalidOutputError{Provider: "gemini", Err: err}
	}
	output, canonicalJSON, err := ValidateStructuredOutput(req.Task, outputJSON)
	if err != nil {
		return GenerateResult{}, &InvalidOutputError{Provider: "gemini", Err: err}
	}

	return GenerateResult{
		Output:      output,
		OutputJSON:  canonicalJSON,
		RawResponse: responseBody,
	}, nil
}

func extractGeminiOutputJSON(responseBody []byte) ([]byte, error) {
	var response geminiResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode gemini response: %w", err)
	}

	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				return []byte(part.Text), nil
			}
		}
	}

	return nil, fmt.Errorf("gemini response does not contain text content")
}
