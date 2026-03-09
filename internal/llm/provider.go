package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/soudai/BacklogTracker/internal/config"
)

const (
	defaultOpenAIEndpoint = "https://api.openai.com/v1/responses"
	defaultGeminiBaseURL  = "https://generativelanguage.googleapis.com/v1beta"
)

type Provider interface {
	Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error)
}

type Option func(*options)

type options struct {
	httpClient     *http.Client
	openAIEndpoint string
	geminiBaseURL  string
	sleep          func(context.Context, time.Duration) error
}

func WithHTTPClient(client *http.Client) Option {
	return func(options *options) {
		options.httpClient = client
	}
}

func WithOpenAIEndpoint(endpoint string) Option {
	return func(options *options) {
		options.openAIEndpoint = endpoint
	}
}

func WithGeminiBaseURL(baseURL string) Option {
	return func(options *options) {
		options.geminiBaseURL = baseURL
	}
}

func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(options *options) {
		options.sleep = sleep
	}
}

func NewFromConfig(cfg config.Config, optionFns ...Option) (Provider, error) {
	opts := options{
		httpClient:     &http.Client{Timeout: time.Duration(cfg.LLMTimeoutSeconds) * time.Second},
		openAIEndpoint: defaultOpenAIEndpoint,
		geminiBaseURL:  defaultGeminiBaseURL,
		sleep:          sleepWithContext,
	}
	for _, optionFn := range optionFns {
		optionFn(&opts)
	}
	if opts.httpClient == nil {
		opts.httpClient = &http.Client{Timeout: time.Duration(cfg.LLMTimeoutSeconds) * time.Second}
	}
	if opts.sleep == nil {
		opts.sleep = sleepWithContext
	}

	switch cfg.LLMProvider {
	case config.ProviderChatGPT:
		if err := cfg.ValidateProviderCredentials(); err != nil {
			return nil, err
		}
		return &openAIProvider{
			httpClient: opts.httpClient,
			endpoint:   opts.openAIEndpoint,
			apiKey:     cfg.OpenAIAPIKey,
			model:      cfg.OpenAIModel,
			maxRetries: cfg.LLMMaxRetries,
			sleep:      opts.sleep,
		}, nil
	case config.ProviderGemini:
		if err := cfg.ValidateProviderCredentials(); err != nil {
			return nil, err
		}
		return &geminiProvider{
			httpClient: opts.httpClient,
			baseURL:    opts.geminiBaseURL,
			apiKey:     cfg.GeminiAPIKey,
			model:      cfg.GeminiModel,
			maxRetries: cfg.LLMMaxRetries,
			sleep:      opts.sleep,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider %q", cfg.LLMProvider)
	}
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func summarizeErrorBody(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil {
		return "read error response body failed"
	}
	return string(bytes.TrimSpace(data))
}

func postJSON(ctx context.Context, httpClient *http.Client, method, endpoint string, headers map[string]string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if response.StatusCode/100 != 2 {
		return nil, &HTTPStatusError{
			Status:     response.Status,
			StatusCode: response.StatusCode,
			Method:     request.Method,
			URL:        sanitizeURL(request.URL.String()),
			Body:       summarizeErrorBody(bytes.NewReader(responseBody)),
		}
	}

	return responseBody, nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return time.Duration(attempt) * 200 * time.Millisecond
}
