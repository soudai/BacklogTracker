package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/soudai/BacklogTracker/internal/config"
)

const (
	defaultWebhookEndpoint = ""
	defaultBotEndpoint     = "https://slack.com/api/chat.postMessage"
	defaultTimeout         = 30 * time.Second
	defaultMaxRetries      = 2
)

type Message struct {
	Text   string  `json:"text"`
	Blocks []Block `json:"blocks,omitempty"`
}

type Block map[string]any

type Response struct {
	Destination string
	Summary     string
}

type Notifier interface {
	Send(ctx context.Context, message Message) (Response, error)
}

type Option func(*options)

type options struct {
	httpClient      *http.Client
	webhookEndpoint string
	botEndpoint     string
	maxRetries      int
	sleep           func(context.Context, time.Duration) error
}

type client struct {
	httpClient      *http.Client
	webhookEndpoint string
	botEndpoint     string
	maxRetries      int
	sleep           func(context.Context, time.Duration) error
	webhookURL      string
	botToken        string
	channel         string
}

type HTTPStatusError struct {
	Status     string
	StatusCode int
	Method     string
	URL        string
	Body       string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Body != "" {
		return fmt.Sprintf("slack request failed: %s %s returned %s: %s", e.Method, sanitizeURL(e.URL), e.Status, e.Body)
	}
	return fmt.Sprintf("slack request failed: %s %s returned %s", e.Method, sanitizeURL(e.URL), e.Status)
}

func (e *HTTPStatusError) HTTPStatusCode() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

type APIError struct {
	Method string
	URL    string
	Code   string
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("slack api request failed: %s %s returned %s", e.Method, sanitizeURL(e.URL), e.Code)
}

func NewFromConfig(cfg config.Config, optionFns ...Option) (Notifier, error) {
	opts := options{
		httpClient:      &http.Client{Timeout: defaultTimeout},
		webhookEndpoint: defaultWebhookEndpoint,
		botEndpoint:     defaultBotEndpoint,
		maxRetries:      defaultMaxRetries,
		sleep:           sleepWithContext,
	}
	for _, optionFn := range optionFns {
		optionFn(&opts)
	}
	if opts.httpClient == nil {
		opts.httpClient = &http.Client{Timeout: defaultTimeout}
	}
	if opts.sleep == nil {
		opts.sleep = sleepWithContext
	}
	if opts.maxRetries < 0 {
		return nil, fmt.Errorf("slack max retries must be greater than or equal to 0")
	}

	if strings.TrimSpace(cfg.SlackWebhookURL) != "" {
		return &client{
			httpClient:      opts.httpClient,
			webhookEndpoint: opts.webhookEndpoint,
			maxRetries:      opts.maxRetries,
			sleep:           opts.sleep,
			webhookURL:      cfg.SlackWebhookURL,
		}, nil
	}
	if strings.TrimSpace(cfg.SlackBotToken) == "" {
		return nil, fmt.Errorf("SLACK_WEBHOOK_URL or SLACK_BOT_TOKEN is required")
	}
	if strings.TrimSpace(cfg.SlackChannel) == "" {
		return nil, fmt.Errorf("SLACK_CHANNEL is required when using SLACK_BOT_TOKEN")
	}

	return &client{
		httpClient:  opts.httpClient,
		botEndpoint: opts.botEndpoint,
		maxRetries:  opts.maxRetries,
		sleep:       opts.sleep,
		botToken:    cfg.SlackBotToken,
		channel:     cfg.SlackChannel,
	}, nil
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(opts *options) {
		opts.httpClient = httpClient
	}
}

func WithWebhookEndpoint(endpoint string) Option {
	return func(opts *options) {
		opts.webhookEndpoint = endpoint
	}
}

func WithBotEndpoint(endpoint string) Option {
	return func(opts *options) {
		opts.botEndpoint = endpoint
	}
}

func WithMaxRetries(maxRetries int) Option {
	return func(opts *options) {
		opts.maxRetries = maxRetries
	}
}

func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(opts *options) {
		opts.sleep = sleep
	}
}

func (c *client) Send(ctx context.Context, message Message) (Response, error) {
	if strings.TrimSpace(message.Text) == "" {
		return Response{}, fmt.Errorf("slack message text is required")
	}
	if strings.TrimSpace(c.webhookURL) != "" {
		return c.sendWebhook(ctx, message)
	}
	return c.sendBotMessage(ctx, message)
}

func (c *client) sendWebhook(ctx context.Context, message Message) (Response, error) {
	endpoint := c.webhookURL
	if strings.TrimSpace(c.webhookEndpoint) != "" {
		endpoint = c.webhookEndpoint
	}

	var responseSummary string
	err := c.doWithRetry(ctx, func(ctx context.Context) error {
		payload, err := json.Marshal(message)
		if err != nil {
			return fmt.Errorf("marshal slack webhook payload: %w", err)
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("build slack webhook request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")

		response, err := c.httpClient.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()

		body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err != nil {
			return fmt.Errorf("read slack webhook response: %w", err)
		}
		responseSummary = strings.TrimSpace(string(body))

		if response.StatusCode/100 != 2 {
			return &HTTPStatusError{
				Status:     response.Status,
				StatusCode: response.StatusCode,
				Method:     request.Method,
				URL:        request.URL.String(),
				Body:       responseSummary,
			}
		}
		if responseSummary == "" {
			responseSummary = response.Status
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}

	return Response{
		Destination: "incoming-webhook",
		Summary:     responseSummary,
	}, nil
}

func (c *client) sendBotMessage(ctx context.Context, message Message) (Response, error) {
	endpoint := c.botEndpoint
	if strings.TrimSpace(endpoint) == "" {
		endpoint = defaultBotEndpoint
	}

	payload := struct {
		Channel string  `json:"channel"`
		Text    string  `json:"text"`
		Blocks  []Block `json:"blocks,omitempty"`
	}{
		Channel: c.channel,
		Text:    message.Text,
		Blocks:  message.Blocks,
	}

	var responseSummary string
	err := c.doWithRetry(ctx, func(ctx context.Context) error {
		body, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal slack bot payload: %w", err)
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build slack bot request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+c.botToken)

		response, err := c.httpClient.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()

		responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err != nil {
			return fmt.Errorf("read slack bot response: %w", err)
		}
		responseSummary = strings.TrimSpace(string(responseBody))

		if response.StatusCode/100 != 2 {
			return &HTTPStatusError{
				Status:     response.Status,
				StatusCode: response.StatusCode,
				Method:     request.Method,
				URL:        request.URL.String(),
				Body:       responseSummary,
			}
		}

		var decoded struct {
			OK      bool   `json:"ok"`
			Error   string `json:"error"`
			Channel string `json:"channel"`
			TS      string `json:"ts"`
		}
		if err := json.Unmarshal(responseBody, &decoded); err != nil {
			return fmt.Errorf("decode slack bot response: %w", err)
		}
		if !decoded.OK {
			return &APIError{
				Method: request.Method,
				URL:    request.URL.String(),
				Code:   decoded.Error,
			}
		}

		responseSummary = fmt.Sprintf("channel=%s ts=%s", decoded.Channel, decoded.TS)
		return nil
	})
	if err != nil {
		return Response{}, err
	}

	return Response{
		Destination: c.channel,
		Summary:     responseSummary,
	}, nil
}

func (c *client) doWithRetry(ctx context.Context, fn func(context.Context) error) error {
	attempts := c.maxRetries + 1
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == attempts || !IsTemporaryError(err) {
			break
		}
		if err := c.sleep(ctx, retryDelay(attempt)); err != nil {
			return err
		}
	}
	return lastErr
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

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return time.Duration(attempt) * 200 * time.Millisecond
}

func IsTemporaryError(err error) bool {
	statusCode, ok := StatusCode(err)
	return ok && (statusCode == http.StatusTooManyRequests || statusCode >= 500)
}

func StatusCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var coded interface{ HTTPStatusCode() int }
	if errors.As(err, &coded) {
		return coded.HTTPStatusCode(), true
	}
	return 0, false
}

func sanitizeURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		prefix, _, _ := strings.Cut(raw, "?")
		prefix, _, _ = strings.Cut(prefix, "#")
		return prefix
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimSuffix(parsed.String(), "/")
}
