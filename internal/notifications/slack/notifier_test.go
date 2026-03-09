package slack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/soudai/BacklogTracker/internal/config"
)

func TestWebhookNotifierSendsBlockKitPayload(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got, want := payload["text"], "summary"; got != want {
			t.Fatalf("text = %#v, want %q", got, want)
		}
		if _, ok := payload["blocks"]; !ok {
			t.Fatalf("payload missing blocks")
		}

		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	notifier, err := NewFromConfig(config.Config{
		SlackWebhookURL: server.URL,
	}, WithMaxRetries(0))
	if err != nil {
		t.Fatalf("NewFromConfig returned error: %v", err)
	}

	response, err := notifier.Send(context.Background(), Message{
		Text: "summary",
		Blocks: []Block{
			{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": "hello",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if got, want := response.Destination, "incoming-webhook"; got != want {
		t.Fatalf("response.Destination = %q, want %q", got, want)
	}
	if got, want := response.Summary, "ok"; got != want {
		t.Fatalf("response.Summary = %q, want %q", got, want)
	}
}

func TestNewFromConfigRequiresChannelForBotToken(t *testing.T) {
	t.Parallel()

	_, err := NewFromConfig(config.Config{
		SlackBotToken: "xoxb-test",
	})
	if err == nil {
		t.Fatalf("NewFromConfig expected error")
	}
	if got, want := err.Error(), "SLACK_CHANNEL is required when using SLACK_BOT_TOKEN"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestHTTPStatusErrorRedactsWebhookPath(t *testing.T) {
	t.Parallel()

	err := &HTTPStatusError{
		Method: "POST",
		URL:    "https://hooks.slack.com/services/T000/B000/SECRET?foo=bar#frag",
		Status: "403 Forbidden",
	}
	message := err.Error()
	if strings.Contains(message, "/services/") || strings.Contains(message, "SECRET") {
		t.Fatalf("error message leaked webhook path: %q", message)
	}
	if !strings.Contains(message, "https://hooks.slack.com") {
		t.Fatalf("error message = %q, want host-only URL", message)
	}
}

func TestIsTemporaryErrorRecognizesContextAndNetErrors(t *testing.T) {
	t.Parallel()

	if !IsTemporaryError(context.DeadlineExceeded) {
		t.Fatalf("context.DeadlineExceeded should be temporary")
	}

	timeoutErr := &temporaryNetError{timeout: true}
	if !IsTemporaryError(timeoutErr) {
		t.Fatalf("timeout net error should be temporary")
	}

	wrapped := errors.New("wrapper: " + timeoutErr.Error())
	if IsTemporaryError(wrapped) {
		t.Fatalf("plain wrapped string error should not be temporary")
	}
}

func TestWebhookNotifierRetriesTemporaryTransportErrors(t *testing.T) {
	t.Parallel()

	attempts := 0
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return nil, &temporaryNetError{timeout: true}
			}
			recorder := httptest.NewRecorder()
			recorder.WriteHeader(http.StatusOK)
			_, _ = recorder.WriteString("ok")
			return recorder.Result(), nil
		}),
		Timeout: time.Second,
	}

	notifier, err := NewFromConfig(config.Config{
		SlackWebhookURL: "https://hooks.slack.com/services/T000/B000/SECRET",
	}, WithHTTPClient(httpClient), WithMaxRetries(1), WithSleep(func(context.Context, time.Duration) error { return nil }))
	if err != nil {
		t.Fatalf("NewFromConfig returned error: %v", err)
	}

	response, err := notifier.Send(context.Background(), Message{Text: "summary"})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if got, want := attempts, 2; got != want {
		t.Fatalf("attempts = %d, want %d", got, want)
	}
	if got, want := response.Summary, "ok"; got != want {
		t.Fatalf("response.Summary = %q, want %q", got, want)
	}
}

type temporaryNetError struct {
	timeout bool
}

func (e *temporaryNetError) Error() string   { return "temporary transport error" }
func (e *temporaryNetError) Timeout() bool   { return e.timeout }
func (e *temporaryNetError) Temporary() bool { return true }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
