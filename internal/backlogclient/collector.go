package backlogclient

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ErrAssigneeNotFound = errors.New("assignee not found")

type collectorAPI interface {
	ListProjectUsers(ctx context.Context, projectIDOrKey string) ([]User, error)
	ListIssues(ctx context.Context, input IssueListInput) ([]Issue, error)
}

type Collector struct {
	client collectorAPI
}

type AssigneeIssueInput struct {
	ProjectIDOrKey string
	Account        string
	StatusIDs      []int
	DateField      IssueDateField
	From           time.Time
	To             time.Time
	PageSize       int
}

func NewCollector(client collectorAPI) *Collector {
	return &Collector{client: client}
}

func (c *Collector) CollectPeriodIssues(ctx context.Context, input IssueListInput) ([]Issue, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("collector client is required")
	}
	return c.client.ListIssues(ctx, input)
}

func (c *Collector) ResolveAssignee(ctx context.Context, projectIDOrKey, account string) (User, error) {
	if c == nil || c.client == nil {
		return User{}, fmt.Errorf("collector client is required")
	}
	if strings.TrimSpace(projectIDOrKey) == "" {
		return User{}, fmt.Errorf("projectIDOrKey is required")
	}
	if strings.TrimSpace(account) == "" {
		return User{}, fmt.Errorf("account is required")
	}

	users, err := c.client.ListProjectUsers(ctx, projectIDOrKey)
	if err != nil {
		return User{}, fmt.Errorf("list project users for %s: %w", projectIDOrKey, err)
	}

	target := strings.TrimSpace(account)
	for _, user := range users {
		if userMatchesAccount(user, target) {
			return user, nil
		}
	}

	return User{}, fmt.Errorf("%w: %s", ErrAssigneeNotFound, account)
}

func (c *Collector) CollectAssigneeIssues(ctx context.Context, input AssigneeIssueInput) ([]Issue, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("collector client is required")
	}
	assignee, err := c.ResolveAssignee(ctx, input.ProjectIDOrKey, input.Account)
	if err != nil {
		return nil, err
	}

	return c.client.ListIssues(ctx, IssueListInput{
		ProjectIDOrKey: input.ProjectIDOrKey,
		AssigneeIDs:    []int{assignee.ID},
		StatusIDs:      append([]int(nil), input.StatusIDs...),
		DateField:      input.DateField,
		From:           input.From,
		To:             input.To,
		PageSize:       input.PageSize,
	})
}

func userMatchesAccount(user User, target string) bool {
	if strings.EqualFold(user.UserID, target) {
		return true
	}
	if strings.EqualFold(user.UniqueID, target) {
		return true
	}
	if strings.EqualFold(user.Name, target) {
		return true
	}
	if strings.EqualFold(user.MailAddress, target) {
		return true
	}
	if localPart, _, ok := strings.Cut(user.MailAddress, "@"); ok && strings.EqualFold(localPart, target) {
		return true
	}
	return user.ID > 0 && strconv.Itoa(user.ID) == target
}
