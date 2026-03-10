package backlogclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeBaseURL(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{
			name: "strips trailing slash",
			raw:  "https://example.backlog.com/",
			want: "https://example.backlog.com",
		},
		{
			name: "strips api v2 suffix",
			raw:  "https://example.backlog.com/api/v2/",
			want: "https://example.backlog.com",
		},
		{
			name: "preserves subpath",
			raw:  "https://example.backlog.com/backlog/api/v2",
			want: "https://example.backlog.com/backlog",
		},
		{
			name:    "requires host",
			raw:     "/api/v2",
			wantErr: "must include scheme and host",
		},
		{
			name:    "rejects query",
			raw:     "https://example.backlog.com?foo=bar",
			wantErr: "must not include query or fragment",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeBaseURL(testCase.raw)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("NormalizeBaseURL error = %v, want substring %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeBaseURL returned error: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("NormalizeBaseURL = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestClientListsProjectDataIssuesAndComments(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v2/projects/PROJ":
			if got, want := r.URL.Query().Get("apiKey"), "test-key"; got != want {
				t.Errorf("apiKey = %q, want %q", got, want)
				http.Error(w, "bad apiKey", http.StatusBadRequest)
				return
			}
			writeJSON(t, w, map[string]any{
				"id":         1,
				"projectKey": "PROJ",
				"name":       "Project",
			})
		case "/api/v2/projects/PROJ/users":
			writeJSON(t, w, []map[string]any{
				{
					"id":          10,
					"userId":      "alice",
					"name":        "Alice",
					"keyword":     "Alice Example",
					"mailAddress": "alice@example.com",
					"nulabAccount": map[string]any{
						"uniqueId": "alice-example",
					},
				},
			})
		case "/api/v2/projects/PROJ/statuses":
			writeJSON(t, w, []map[string]any{
				{
					"id":           2,
					"projectId":    1,
					"name":         "In Progress",
					"color":        "#ed8077",
					"displayOrder": 10,
				},
			})
		case "/api/v2/issues":
			query := r.URL.Query()
			if got, want := query.Get("projectId[]"), "1"; got != want {
				t.Errorf("projectId[] = %q, want %q", got, want)
				http.Error(w, "bad projectId", http.StatusBadRequest)
				return
			}
			if got, want := query.Get("assigneeId[]"), "10"; got != want {
				t.Errorf("assigneeId[] = %q, want %q", got, want)
				http.Error(w, "bad assigneeId", http.StatusBadRequest)
				return
			}
			if got, want := query.Get("statusId[]"), "2"; got != want {
				t.Errorf("statusId[] = %q, want %q", got, want)
				http.Error(w, "bad statusId", http.StatusBadRequest)
				return
			}
			if got, want := query.Get("updatedSince"), "2026-01-01"; got != want {
				t.Errorf("updatedSince = %q, want %q", got, want)
				http.Error(w, "bad updatedSince", http.StatusBadRequest)
				return
			}
			if got, want := query.Get("updatedUntil"), "2026-01-31"; got != want {
				t.Errorf("updatedUntil = %q, want %q", got, want)
				http.Error(w, "bad updatedUntil", http.StatusBadRequest)
				return
			}
			if got, want := query.Get("count"), "2"; got != want {
				t.Errorf("count = %q, want %q", got, want)
				http.Error(w, "bad count", http.StatusBadRequest)
				return
			}

			switch query.Get("offset") {
			case "0":
				writeJSON(t, w, []map[string]any{
					{
						"id":          1001,
						"projectId":   1,
						"issueKey":    "PROJ-1",
						"summary":     "First issue",
						"description": "details",
						"status": map[string]any{
							"id":           2,
							"projectId":    1,
							"name":         "In Progress",
							"displayOrder": 10,
						},
						"assignee": map[string]any{
							"id":     10,
							"userId": "alice",
							"name":   "Alice",
						},
						"created": "2026-01-01T00:00:00Z",
						"updated": "2026-01-02T00:00:00Z",
					},
					{
						"id":          1002,
						"projectId":   1,
						"issueKey":    "PROJ-2",
						"summary":     "Second issue",
						"description": "details",
						"created":     "2026-01-03T00:00:00Z",
						"updated":     "2026-01-04T00:00:00Z",
					},
				})
			case "2":
				writeJSON(t, w, []map[string]any{
					{
						"id":          1003,
						"projectId":   1,
						"issueKey":    "PROJ-3",
						"summary":     "Third issue",
						"description": "details",
						"created":     "2026-01-05T00:00:00Z",
						"updated":     "2026-01-06T00:00:00Z",
					},
				})
			default:
				t.Errorf("unexpected offset = %q", query.Get("offset"))
				http.Error(w, "bad offset", http.StatusBadRequest)
				return
			}
		case "/api/v2/issues/PROJ-1/comments":
			writeJSON(t, w, []map[string]any{
				{
					"id":      5001,
					"content": "Looks good",
					"createdUser": map[string]any{
						"id":     11,
						"userId": "bob",
						"name":   "Bob",
					},
					"created": "2026-01-02T09:00:00Z",
					"updated": "2026-01-02T09:30:00Z",
				},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	client, err := New("test-key", server.URL+"/api/v2/")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	project, err := client.CheckConnection(context.Background(), "PROJ")
	if err != nil {
		t.Fatalf("CheckConnection returned error: %v", err)
	}
	if got, want := project.Key, "PROJ"; got != want {
		t.Fatalf("project.Key = %q, want %q", got, want)
	}

	users, err := client.ListProjectUsers(context.Background(), "PROJ")
	if err != nil {
		t.Fatalf("ListProjectUsers returned error: %v", err)
	}
	if len(users) != 1 || users[0].UserID != "alice" {
		t.Fatalf("ListProjectUsers = %#v, want one alice user", users)
	}
	if got, want := users[0].UniqueID, "alice-example"; got != want {
		t.Fatalf("users[0].UniqueID = %q, want %q", got, want)
	}

	statuses, err := client.ListProjectStatuses(context.Background(), "PROJ")
	if err != nil {
		t.Fatalf("ListProjectStatuses returned error: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Name != "In Progress" {
		t.Fatalf("ListProjectStatuses = %#v, want one status", statuses)
	}

	issues, err := client.ListIssues(context.Background(), IssueListInput{
		ProjectIDOrKey: "PROJ",
		AssigneeIDs:    []int{10},
		StatusIDs:      []int{2},
		DateField:      IssueDateFieldUpdated,
		From:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To:             time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
		PageSize:       2,
	})
	if err != nil {
		t.Fatalf("ListIssues returned error: %v", err)
	}
	if got, want := len(issues), 3; got != want {
		t.Fatalf("len(issues) = %d, want %d", got, want)
	}
	if got, want := issues[0].IssueKey, "PROJ-1"; got != want {
		t.Fatalf("issues[0].IssueKey = %q, want %q", got, want)
	}
	if issues[0].Assignee == nil || issues[0].Assignee.UserID != "alice" {
		t.Fatalf("issues[0].Assignee = %#v, want alice", issues[0].Assignee)
	}

	comments, err := client.ListIssueComments(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatalf("ListIssueComments returned error: %v", err)
	}
	if len(comments) != 1 || comments[0].CreatedUser == nil || comments[0].CreatedUser.UserID != "bob" {
		t.Fatalf("ListIssueComments = %#v, want Bob comment", comments)
	}
}

func TestClientClassifiesHTTPFailures(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		statusCode     int
		wantAuth       bool
		wantTemporary  bool
		wantStatusCode int
	}{
		{
			name:           "authentication error",
			statusCode:     http.StatusUnauthorized,
			wantAuth:       true,
			wantStatusCode: http.StatusUnauthorized,
		},
		{
			name:           "temporary error",
			statusCode:     http.StatusTooManyRequests,
			wantTemporary:  true,
			wantStatusCode: http.StatusTooManyRequests,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, `{"errors":[{"message":"failed","code":11,"moreInfo":"..." }]}`, testCase.statusCode)
			}))
			defer server.Close()

			client, err := New("test-key", server.URL)
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}

			_, err = client.CheckConnection(context.Background(), "PROJ")
			if err == nil {
				t.Fatalf("CheckConnection expected error")
			}

			var httpErr *HTTPStatusError
			if !errors.As(err, &httpErr) {
				t.Fatalf("error = %v, want HTTPStatusError", err)
			}
			if strings.Contains(httpErr.URL, "apiKey=") {
				t.Fatalf("HTTPStatusError.URL leaked query string: %q", httpErr.URL)
			}

			statusCode, ok := StatusCode(err)
			if !ok {
				t.Fatalf("StatusCode(%v) = not found, want %d", err, testCase.wantStatusCode)
			}
			if statusCode != testCase.wantStatusCode {
				t.Fatalf("StatusCode(%v) = %d, want %d", err, statusCode, testCase.wantStatusCode)
			}
			if got := IsAuthenticationError(err); got != testCase.wantAuth {
				t.Fatalf("IsAuthenticationError(%v) = %t, want %t", err, got, testCase.wantAuth)
			}
			if got := IsTemporaryError(err); got != testCase.wantTemporary {
				t.Fatalf("IsTemporaryError(%v) = %t, want %t", err, got, testCase.wantTemporary)
			}
		})
	}
}

func TestBuildProjectUsersURLPreservesBaseSubpath(t *testing.T) {
	t.Parallel()

	client := &Client{
		baseURL: "https://example.backlog.com/backlog",
		apiKey:  "test-key",
	}

	got, err := client.buildProjectUsersURL("PROJ")
	if err != nil {
		t.Fatalf("buildProjectUsersURL returned error: %v", err)
	}
	if got != "https://example.backlog.com/backlog/api/v2/projects/PROJ/users?apiKey=test-key" {
		t.Fatalf("buildProjectUsersURL = %q", got)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()

	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}
