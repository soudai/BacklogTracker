package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

type HTTPStatusError struct {
	Provider   string
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
		return fmt.Sprintf("%s request failed: %s %s returned %s: %s", e.Provider, e.Method, e.URL, e.Status, e.Body)
	}
	return fmt.Sprintf("%s request failed: %s %s returned %s", e.Provider, e.Method, e.URL, e.Status)
}

func (e *HTTPStatusError) HTTPStatusCode() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

type InvalidOutputError struct {
	Provider string
	Err      error
}

func (e *InvalidOutputError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s returned invalid structured output: %v", e.Provider, e.Err)
}

func (e *InvalidOutputError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
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

func IsTemporaryError(err error) bool {
	statusCode, ok := StatusCode(err)
	if ok && (statusCode == 429 || (statusCode >= 500 && statusCode <= 599)) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}

func ShouldRetry(err error) bool {
	if err == nil {
		return false
	}
	if IsTemporaryError(err) {
		return true
	}
	var invalidOutput *InvalidOutputError
	return errors.As(err, &invalidOutput)
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
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
