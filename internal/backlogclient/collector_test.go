package backlogclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCollectorResolveAssignee(t *testing.T) {
	t.Parallel()

	collector := NewCollector(&fakeCollectorAPI{
		users: []User{
			{ID: 10, UserID: "alice", Name: "Alice"},
			{ID: 20, UserID: "bob", Name: "Bob"},
		},
	})

	user, err := collector.ResolveAssignee(context.Background(), "PROJ", "alice")
	if err != nil {
		t.Fatalf("ResolveAssignee returned error: %v", err)
	}
	if got, want := user.ID, 10; got != want {
		t.Fatalf("user.ID = %d, want %d", got, want)
	}

	user, err = collector.ResolveAssignee(context.Background(), "PROJ", "20")
	if err != nil {
		t.Fatalf("ResolveAssignee returned error: %v", err)
	}
	if got, want := user.UserID, "bob"; got != want {
		t.Fatalf("user.UserID = %q, want %q", got, want)
	}
}

func TestCollectorResolveAssigneeNotFound(t *testing.T) {
	t.Parallel()

	collector := NewCollector(&fakeCollectorAPI{})

	_, err := collector.ResolveAssignee(context.Background(), "PROJ", "alice")
	if !errors.Is(err, ErrAssigneeNotFound) {
		t.Fatalf("ResolveAssignee error = %v, want ErrAssigneeNotFound", err)
	}
}

func TestCollectorCollectAssigneeIssues(t *testing.T) {
	t.Parallel()

	api := fakeCollectorAPI{
		users: []User{
			{ID: 10, UserID: "alice", Name: "Alice"},
		},
		issues: []Issue{
			{ID: 1, IssueKey: "PROJ-1"},
		},
	}
	collector := NewCollector(&api)

	issues, err := collector.CollectAssigneeIssues(context.Background(), AssigneeIssueInput{
		ProjectIDOrKey: "PROJ",
		Account:        "alice",
		StatusIDs:      []int{2},
		DateField:      IssueDateFieldCreated,
		From:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To:             time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
		PageSize:       10,
	})
	if err != nil {
		t.Fatalf("CollectAssigneeIssues returned error: %v", err)
	}

	if got, want := len(issues), 1; got != want {
		t.Fatalf("len(issues) = %d, want %d", got, want)
	}
	if got, want := api.lastIssueInput.AssigneeIDs, []int{10}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("AssigneeIDs = %#v, want %#v", got, want)
	}
	if got, want := api.lastIssueInput.DateField, IssueDateFieldCreated; got != want {
		t.Fatalf("DateField = %q, want %q", got, want)
	}
}

type fakeCollectorAPI struct {
	users          []User
	issues         []Issue
	listUsersErr   error
	listIssuesErr  error
	lastIssueInput IssueListInput
}

func (f *fakeCollectorAPI) ListProjectUsers(ctx context.Context, projectIDOrKey string) ([]User, error) {
	if f.listUsersErr != nil {
		return nil, f.listUsersErr
	}
	return f.users, nil
}

func (f *fakeCollectorAPI) ListIssues(ctx context.Context, input IssueListInput) ([]Issue, error) {
	if f.listIssuesErr != nil {
		return nil, f.listIssuesErr
	}
	f.lastIssueInput = input
	return f.issues, nil
}
