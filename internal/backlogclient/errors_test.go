package backlogclient

import (
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
