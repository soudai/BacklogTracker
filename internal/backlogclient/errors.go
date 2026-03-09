package backlogclient

import (
	"context"
	"errors"
	"fmt"
	"net"
)

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
		return fmt.Sprintf("backlog request failed: %s %s returned %s: %s", e.Method, e.URL, e.Status, e.Body)
	}
	return fmt.Sprintf("backlog request failed: %s %s returned %s", e.Method, e.URL, e.Status)
}

func (e *HTTPStatusError) HTTPStatusCode() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
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

func IsAuthenticationError(err error) bool {
	statusCode, ok := StatusCode(err)
	return ok && (statusCode == 401 || statusCode == 403)
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
