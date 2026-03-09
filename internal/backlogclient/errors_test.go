package backlogclient

import (
	"errors"
	"strings"
	"testing"
)

func TestHTTPStatusErrorErrorRedactsQueryString(t *testing.T) {
	t.Parallel()

	err := (&HTTPStatusError{
		Status:     "401 Unauthorized",
		StatusCode: 401,
		Method:     "GET",
		URL:        "https://example.backlog.com/api/v2/projects/PROJ?apiKey=secret-token&foo=bar",
		Body:       "failed",
	}).Error()

	if strings.Contains(err, "secret-token") {
		t.Fatalf("Error() leaked api key: %q", err)
	}
	if strings.Contains(err, "foo=bar") {
		t.Fatalf("Error() leaked query string: %q", err)
	}
	if !strings.Contains(err, "https://example.backlog.com/api/v2/projects/PROJ") {
		t.Fatalf("Error() = %q, want sanitized URL path", err)
	}
}

func TestSanitizeURLRemovesFragmentOnParseFallback(t *testing.T) {
	t.Parallel()

	got := sanitizeURL("https://example.backlog.com/%zz?apiKey=secret-token#fragment")
	if strings.Contains(got, "secret-token") {
		t.Fatalf("sanitizeURL leaked query string: %q", got)
	}
	if strings.Contains(got, "#fragment") {
		t.Fatalf("sanitizeURL leaked fragment: %q", got)
	}
}

func TestClientReturnsSanitizedHTTPStatusErrorURL(t *testing.T) {
	t.Parallel()

	err := (&HTTPStatusError{
		Status:     "401 Unauthorized",
		StatusCode: 401,
		Method:     "GET",
		URL:        sanitizeURL("https://example.backlog.com/api/v2/projects/PROJ?apiKey=secret-token#frag"),
	}).Error()

	if strings.Contains(err, "secret-token") || strings.Contains(err, "#frag") {
		t.Fatalf("Error() leaked sanitized parts: %q", err)
	}
}

func TestStatusCodeFindsWrappedHTTPStatusError(t *testing.T) {
	t.Parallel()

	wrapped := errors.New("outer")
	err := errors.Join(wrapped, &HTTPStatusError{StatusCode: 429})

	statusCode, ok := StatusCode(err)
	if !ok || statusCode != 429 {
		t.Fatalf("StatusCode(%v) = (%d, %t), want (429, true)", err, statusCode, ok)
	}
}
