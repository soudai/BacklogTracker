package backlogclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/kenzo0107/backlog"
)

const (
	defaultIssuePageSize = 100
	defaultHTTPTimeout   = 30 * time.Second
	backlogDateLayout    = "2006-01-02"
)

type IssueDateField string

const (
	IssueDateFieldUpdated IssueDateField = "updated"
	IssueDateFieldCreated IssueDateField = "created"
)

type Project struct {
	ID   int
	Key  string
	Name string
}

type User struct {
	ID          int
	UserID      string
	Name        string
	MailAddress string
}

type Status struct {
	ID           int
	ProjectID    int
	Name         string
	Color        string
	DisplayOrder int
}

type Issue struct {
	ID          int
	ProjectID   int
	IssueKey    string
	KeyID       int
	Summary     string
	Description string
	Status      *Status
	Assignee    *User
	CreatedUser *User
	UpdatedUser *User
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type IssueComment struct {
	ID          int
	Content     string
	CreatedUser *User
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type IssueListInput struct {
	ProjectIDOrKey string
	AssigneeIDs    []int
	StatusIDs      []int
	DateField      IssueDateField
	From           time.Time
	To             time.Time
	PageSize       int
}

type Option func(*clientOptions)

type clientOptions struct {
	httpClient *http.Client
}

type apiClient interface {
	GetProjectContext(ctx context.Context, projectIDOrKey interface{}) (*backlog.Project, error)
	GetProjectUsersContext(ctx context.Context, projectIDOrKey interface{}, opts *backlog.GetProjectUsersOptions) ([]*backlog.User, error)
	GetStatusesContext(ctx context.Context, projectIDOrKey interface{}) ([]*backlog.Status, error)
	GetIssuesContext(ctx context.Context, opts *backlog.GetIssuesOptions) ([]*backlog.Issue, error)
	GetIssueCommentsContext(ctx context.Context, issueIDOrKey string, opts *backlog.GetIssueCommentsOptions) ([]*backlog.IssueComment, error)
}

type Client struct {
	api apiClient
}

func WithHTTPClient(client *http.Client) Option {
	return func(options *clientOptions) {
		options.httpClient = client
	}
}

func New(apiKey, baseURL string, options ...Option) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("backlog api key is required")
	}

	normalizedBaseURL, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	settings := clientOptions{
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
	}
	for _, option := range options {
		option(&settings)
	}
	if settings.httpClient == nil {
		settings.httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}

	client := backlog.New(
		apiKey,
		normalizedBaseURL,
		backlog.OptionHTTPClient(&statusCheckingHTTPClient{client: settings.httpClient}),
	)

	return &Client{api: client}, nil
}

func NormalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("backlog base URL is required")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse backlog base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("backlog base URL must include scheme and host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("backlog base URL must not include query or fragment")
	}

	cleanPath := strings.TrimSuffix(path.Clean(parsed.Path), "/")
	if cleanPath == "." {
		cleanPath = ""
	}
	cleanPath = strings.TrimSuffix(cleanPath, "/api/v2")
	if cleanPath == "." || cleanPath == "/" {
		cleanPath = ""
	}
	parsed.Path = cleanPath
	parsed.RawPath = ""

	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func (c *Client) CheckConnection(ctx context.Context, projectIDOrKey string) (Project, error) {
	if c == nil || c.api == nil {
		return Project{}, fmt.Errorf("backlog client is not configured")
	}

	project, err := c.getProject(ctx, projectIDOrKey)
	if err != nil {
		return Project{}, err
	}
	return mapProject(project), nil
}

func (c *Client) ListProjectUsers(ctx context.Context, projectIDOrKey string) ([]User, error) {
	if c == nil || c.api == nil {
		return nil, fmt.Errorf("backlog client is not configured")
	}
	if strings.TrimSpace(projectIDOrKey) == "" {
		return nil, fmt.Errorf("projectIDOrKey is required")
	}

	users, err := c.api.GetProjectUsersContext(ctx, projectIDOrKey, nil)
	if err != nil {
		return nil, fmt.Errorf("get project users for %s: %w", projectIDOrKey, err)
	}

	mapped := make([]User, 0, len(users))
	for _, user := range users {
		mapped = append(mapped, mapUser(user))
	}
	return mapped, nil
}

func (c *Client) ListProjectStatuses(ctx context.Context, projectIDOrKey string) ([]Status, error) {
	if c == nil || c.api == nil {
		return nil, fmt.Errorf("backlog client is not configured")
	}
	if strings.TrimSpace(projectIDOrKey) == "" {
		return nil, fmt.Errorf("projectIDOrKey is required")
	}

	statuses, err := c.api.GetStatusesContext(ctx, projectIDOrKey)
	if err != nil {
		return nil, fmt.Errorf("get statuses for %s: %w", projectIDOrKey, err)
	}

	mapped := make([]Status, 0, len(statuses))
	for _, status := range statuses {
		mapped = append(mapped, mapStatus(status))
	}
	return mapped, nil
}

func (c *Client) ListIssues(ctx context.Context, input IssueListInput) ([]Issue, error) {
	if c == nil || c.api == nil {
		return nil, fmt.Errorf("backlog client is not configured")
	}
	if err := input.Validate(); err != nil {
		return nil, err
	}

	project, err := c.getProject(ctx, input.ProjectIDOrKey)
	if err != nil {
		return nil, err
	}

	pageSize := input.PageSize
	if pageSize == 0 {
		pageSize = defaultIssuePageSize
	}
	dateField := input.normalizedDateField()

	opt := &backlog.GetIssuesOptions{
		ProjectIDs:  []int{derefInt(project.ID)},
		AssigneeIDs: append([]int(nil), input.AssigneeIDs...),
		StatusIDs:   append([]int(nil), input.StatusIDs...),
		Order:       backlog.OrderAsc,
		Count:       backlog.Int(pageSize),
	}
	applyDateRange(opt, dateField, input.From, input.To)

	var issues []Issue
	offset := 0
	for {
		opt.Offset = backlog.Int(offset)

		page, err := c.api.GetIssuesContext(ctx, opt)
		if err != nil {
			return nil, fmt.Errorf("get issues for %s: %w", input.ProjectIDOrKey, err)
		}

		for _, issue := range page {
			issues = append(issues, mapIssue(issue))
		}

		if len(page) < pageSize {
			break
		}
		offset += len(page)
	}

	return issues, nil
}

func (c *Client) ListIssueComments(ctx context.Context, issueIDOrKey string) ([]IssueComment, error) {
	if c == nil || c.api == nil {
		return nil, fmt.Errorf("backlog client is not configured")
	}
	if strings.TrimSpace(issueIDOrKey) == "" {
		return nil, fmt.Errorf("issueIDOrKey is required")
	}

	comments, err := c.api.GetIssueCommentsContext(ctx, issueIDOrKey, nil)
	if err != nil {
		return nil, fmt.Errorf("get issue comments for %s: %w", issueIDOrKey, err)
	}

	mapped := make([]IssueComment, 0, len(comments))
	for _, comment := range comments {
		mapped = append(mapped, mapIssueComment(comment))
	}
	return mapped, nil
}

func (c *Client) getProject(ctx context.Context, projectIDOrKey string) (*backlog.Project, error) {
	if strings.TrimSpace(projectIDOrKey) == "" {
		return nil, fmt.Errorf("projectIDOrKey is required")
	}

	project, err := c.api.GetProjectContext(ctx, projectIDOrKey)
	if err != nil {
		return nil, fmt.Errorf("get project %s: %w", projectIDOrKey, err)
	}
	return project, nil
}

func (i IssueListInput) Validate() error {
	if strings.TrimSpace(i.ProjectIDOrKey) == "" {
		return fmt.Errorf("projectIDOrKey is required")
	}
	if i.PageSize < 0 {
		return fmt.Errorf("pageSize must be greater than or equal to 0")
	}
	if i.PageSize > 0 && i.PageSize > defaultIssuePageSize {
		return fmt.Errorf("pageSize must be less than or equal to %d", defaultIssuePageSize)
	}
	if !i.From.IsZero() && !i.To.IsZero() && i.To.Before(i.From) {
		return fmt.Errorf("to must not be before from")
	}
	for _, assigneeID := range i.AssigneeIDs {
		if assigneeID <= 0 {
			return fmt.Errorf("assigneeIDs must be greater than 0")
		}
	}
	for _, statusID := range i.StatusIDs {
		if statusID <= 0 {
			return fmt.Errorf("statusIDs must be greater than 0")
		}
	}

	switch i.normalizedDateField() {
	case IssueDateFieldUpdated, IssueDateFieldCreated:
		return nil
	default:
		return fmt.Errorf("dateField must be %q or %q", IssueDateFieldUpdated, IssueDateFieldCreated)
	}
}

func (i IssueListInput) normalizedDateField() IssueDateField {
	if i.DateField == "" {
		return IssueDateFieldUpdated
	}
	return i.DateField
}

func applyDateRange(options *backlog.GetIssuesOptions, dateField IssueDateField, from, to time.Time) {
	if !from.IsZero() {
		formatted := from.Format(backlogDateLayout)
		switch dateField {
		case IssueDateFieldCreated:
			options.CreatedSince = &formatted
		default:
			options.UpdatedSince = &formatted
		}
	}
	if !to.IsZero() {
		formatted := to.Format(backlogDateLayout)
		switch dateField {
		case IssueDateFieldCreated:
			options.CreatedUntil = &formatted
		default:
			options.UpdatedUntil = &formatted
		}
	}
}

func mapProject(project *backlog.Project) Project {
	return Project{
		ID:   derefInt(project.ID),
		Key:  derefString(project.ProjectKey),
		Name: derefString(project.Name),
	}
}

func mapUser(user *backlog.User) User {
	if user == nil {
		return User{}
	}

	return User{
		ID:          derefInt(user.ID),
		UserID:      derefString(user.UserID),
		Name:        derefString(user.Name),
		MailAddress: derefString(user.MailAddress),
	}
}

func mapStatus(status *backlog.Status) Status {
	if status == nil {
		return Status{}
	}

	return Status{
		ID:           derefInt(status.ID),
		ProjectID:    derefInt(status.ProjectID),
		Name:         derefString(status.Name),
		Color:        derefString(status.Color),
		DisplayOrder: derefInt(status.DisplayOrder),
	}
}

func mapIssue(issue *backlog.Issue) Issue {
	if issue == nil {
		return Issue{}
	}

	return Issue{
		ID:          derefInt(issue.ID),
		ProjectID:   derefInt(issue.ProjectID),
		IssueKey:    derefString(issue.IssueKey),
		KeyID:       derefInt(issue.KeyID),
		Summary:     derefString(issue.Summary),
		Description: derefString(issue.Description),
		Status:      mapStatusPointer(issue.Status),
		Assignee:    mapUserPointer(issue.Assignee),
		CreatedUser: mapUserPointer(issue.CreatedUser),
		UpdatedUser: mapUserPointer(issue.UpdatedUser),
		CreatedAt:   derefTimestamp(issue.Created),
		UpdatedAt:   derefTimestamp(issue.Updated),
	}
}

func mapIssueComment(comment *backlog.IssueComment) IssueComment {
	if comment == nil {
		return IssueComment{}
	}

	return IssueComment{
		ID:          derefInt(comment.ID),
		Content:     derefString(comment.Content),
		CreatedUser: mapUserPointer(comment.CreatedUser),
		CreatedAt:   derefTimestamp(comment.Created),
		UpdatedAt:   derefTimestamp(comment.Updated),
	}
}

func mapUserPointer(user *backlog.User) *User {
	if user == nil {
		return nil
	}
	mapped := mapUser(user)
	return &mapped
}

func mapStatusPointer(status *backlog.Status) *Status {
	if status == nil {
		return nil
	}
	mapped := mapStatus(status)
	return &mapped
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefTimestamp(value *backlog.Timestamp) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.Time
}

type statusCheckingHTTPClient struct {
	client *http.Client
}

func (c *statusCheckingHTTPClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 == 2 {
		return resp, nil
	}

	bodySummary, readErr := summarizeErrorBody(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		bodySummary = "read error response body failed"
	}

	return nil, &HTTPStatusError{
		Status:     resp.Status,
		StatusCode: resp.StatusCode,
		Method:     req.Method,
		URL:        req.URL.String(),
		Body:       bodySummary,
	}
}

func summarizeErrorBody(body io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil {
		return "", err
	}
	summary := strings.Join(strings.Fields(string(data)), " ")
	return summary, nil
}
