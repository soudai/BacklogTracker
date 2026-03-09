package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/soudai/BacklogTracker/internal/prompts"
)

type TaskOutput interface {
	task() prompts.Task
}

type GenerateRequest struct {
	Task         prompts.Task
	SystemPrompt string
	UserPrompt   string
	SchemaJSON   string
}

type GenerateResult struct {
	Output      TaskOutput
	OutputJSON  []byte
	RawResponse []byte
}

type PeriodSummaryOutput struct {
	ReportType string                  `json:"reportType"`
	Headline   string                  `json:"headline"`
	Overview   string                  `json:"overview"`
	KeyPoints  []string                `json:"keyPoints"`
	RiskItems  []PeriodSummaryRiskItem `json:"riskItems"`
	Counts     PeriodSummaryCounts     `json:"counts"`
}

type PeriodSummaryRiskItem struct {
	IssueKey string `json:"issueKey"`
	Reason   string `json:"reason"`
}

type PeriodSummaryCounts struct {
	Total      int  `json:"total"`
	Open       *int `json:"open,omitempty"`
	InProgress *int `json:"inProgress,omitempty"`
	Resolved   *int `json:"resolved,omitempty"`
	Closed     *int `json:"closed,omitempty"`
}

type AccountReportOutput struct {
	ReportType string               `json:"reportType"`
	Account    AccountReportAccount `json:"account"`
	Summary    string               `json:"summary"`
	Issues     []AccountReportIssue `json:"issues"`
}

type AccountReportAccount struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type AccountReportIssue struct {
	IssueKey           string                          `json:"issueKey"`
	Title              string                          `json:"title"`
	Status             string                          `json:"status"`
	Summary            string                          `json:"summary"`
	ResponseSuggestion AccountReportResponseSuggestion `json:"responseSuggestion"`
}

type AccountReportResponseSuggestion struct {
	Message           string `json:"message"`
	Confidence        string `json:"confidence"`
	NeedsConfirmation bool   `json:"needsConfirmation"`
}

func (PeriodSummaryOutput) task() prompts.Task { return prompts.TaskPeriodSummary }
func (AccountReportOutput) task() prompts.Task { return prompts.TaskAccountReport }

func ValidateStructuredOutput(task prompts.Task, rawJSON []byte) (TaskOutput, []byte, error) {
	switch task {
	case prompts.TaskPeriodSummary:
		output, err := validatePeriodSummaryOutput(rawJSON)
		if err != nil {
			return nil, nil, err
		}
		canonicalJSON, err := json.Marshal(output)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal validated period summary output: %w", err)
		}
		return output, canonicalJSON, nil
	case prompts.TaskAccountReport:
		output, err := validateAccountReportOutput(rawJSON)
		if err != nil {
			return nil, nil, err
		}
		canonicalJSON, err := json.Marshal(output)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal validated account report output: %w", err)
		}
		return output, canonicalJSON, nil
	default:
		return nil, nil, fmt.Errorf("unsupported structured output task %q", task)
	}
}

type periodSummaryOutputWire struct {
	ReportType *string                  `json:"reportType"`
	Headline   *string                  `json:"headline"`
	Overview   *string                  `json:"overview"`
	KeyPoints  []string                 `json:"keyPoints"`
	RiskItems  []periodSummaryRiskWire  `json:"riskItems"`
	Counts     *periodSummaryCountsWire `json:"counts"`
}

type periodSummaryRiskWire struct {
	IssueKey *string `json:"issueKey"`
	Reason   *string `json:"reason"`
}

type periodSummaryCountsWire struct {
	Total      *int `json:"total"`
	Open       *int `json:"open"`
	InProgress *int `json:"inProgress"`
	Resolved   *int `json:"resolved"`
	Closed     *int `json:"closed"`
}

type accountReportOutputWire struct {
	ReportType *string                   `json:"reportType"`
	Account    *accountReportAccountWire `json:"account"`
	Summary    *string                   `json:"summary"`
	Issues     []accountReportIssueWire  `json:"issues"`
}

type accountReportAccountWire struct {
	ID          *string `json:"id"`
	DisplayName *string `json:"displayName"`
}

type accountReportIssueWire struct {
	IssueKey           *string                              `json:"issueKey"`
	Title              *string                              `json:"title"`
	Status             *string                              `json:"status"`
	Summary            *string                              `json:"summary"`
	ResponseSuggestion *accountReportResponseSuggestionWire `json:"responseSuggestion"`
}

type accountReportResponseSuggestionWire struct {
	Message           *string `json:"message"`
	Confidence        *string `json:"confidence"`
	NeedsConfirmation *bool   `json:"needsConfirmation"`
}

func validatePeriodSummaryOutput(rawJSON []byte) (PeriodSummaryOutput, error) {
	var wire periodSummaryOutputWire
	if err := decodeStrictJSON(rawJSON, &wire); err != nil {
		return PeriodSummaryOutput{}, fmt.Errorf("decode period summary output: %w", err)
	}

	if wire.ReportType == nil || *wire.ReportType != "period_summary" {
		return PeriodSummaryOutput{}, fmt.Errorf("reportType must be period_summary")
	}
	if wire.Headline == nil {
		return PeriodSummaryOutput{}, fmt.Errorf("headline is required")
	}
	if wire.Overview == nil {
		return PeriodSummaryOutput{}, fmt.Errorf("overview is required")
	}
	if wire.KeyPoints == nil {
		return PeriodSummaryOutput{}, fmt.Errorf("keyPoints is required")
	}
	if wire.RiskItems == nil {
		return PeriodSummaryOutput{}, fmt.Errorf("riskItems is required")
	}
	if wire.Counts == nil || wire.Counts.Total == nil {
		return PeriodSummaryOutput{}, fmt.Errorf("counts.total is required")
	}

	riskItems := make([]PeriodSummaryRiskItem, 0, len(wire.RiskItems))
	for index, item := range wire.RiskItems {
		if item.IssueKey == nil || item.Reason == nil {
			return PeriodSummaryOutput{}, fmt.Errorf("riskItems[%d].issueKey and riskItems[%d].reason are required", index, index)
		}
		riskItems = append(riskItems, PeriodSummaryRiskItem{
			IssueKey: *item.IssueKey,
			Reason:   *item.Reason,
		})
	}

	return PeriodSummaryOutput{
		ReportType: *wire.ReportType,
		Headline:   *wire.Headline,
		Overview:   *wire.Overview,
		KeyPoints:  append([]string(nil), wire.KeyPoints...),
		RiskItems:  riskItems,
		Counts: PeriodSummaryCounts{
			Total:      *wire.Counts.Total,
			Open:       wire.Counts.Open,
			InProgress: wire.Counts.InProgress,
			Resolved:   wire.Counts.Resolved,
			Closed:     wire.Counts.Closed,
		},
	}, nil
}

func validateAccountReportOutput(rawJSON []byte) (AccountReportOutput, error) {
	var wire accountReportOutputWire
	if err := decodeStrictJSON(rawJSON, &wire); err != nil {
		return AccountReportOutput{}, fmt.Errorf("decode account report output: %w", err)
	}

	if wire.ReportType == nil || *wire.ReportType != "account_report" {
		return AccountReportOutput{}, fmt.Errorf("reportType must be account_report")
	}
	if wire.Account == nil || wire.Account.ID == nil || wire.Account.DisplayName == nil {
		return AccountReportOutput{}, fmt.Errorf("account.id and account.displayName are required")
	}
	if wire.Summary == nil {
		return AccountReportOutput{}, fmt.Errorf("summary is required")
	}
	if wire.Issues == nil {
		return AccountReportOutput{}, fmt.Errorf("issues is required")
	}

	issues := make([]AccountReportIssue, 0, len(wire.Issues))
	for index, item := range wire.Issues {
		if item.IssueKey == nil || item.Title == nil || item.Status == nil || item.Summary == nil || item.ResponseSuggestion == nil {
			return AccountReportOutput{}, fmt.Errorf("issues[%d] is missing required fields", index)
		}
		if item.ResponseSuggestion.Message == nil || item.ResponseSuggestion.Confidence == nil || item.ResponseSuggestion.NeedsConfirmation == nil {
			return AccountReportOutput{}, fmt.Errorf("issues[%d].responseSuggestion is missing required fields", index)
		}
		switch *item.ResponseSuggestion.Confidence {
		case "high", "medium", "low":
		default:
			return AccountReportOutput{}, fmt.Errorf("issues[%d].responseSuggestion.confidence must be high, medium, or low", index)
		}
		issues = append(issues, AccountReportIssue{
			IssueKey: *item.IssueKey,
			Title:    *item.Title,
			Status:   *item.Status,
			Summary:  *item.Summary,
			ResponseSuggestion: AccountReportResponseSuggestion{
				Message:           *item.ResponseSuggestion.Message,
				Confidence:        *item.ResponseSuggestion.Confidence,
				NeedsConfirmation: *item.ResponseSuggestion.NeedsConfirmation,
			},
		})
	}

	return AccountReportOutput{
		ReportType: *wire.ReportType,
		Account: AccountReportAccount{
			ID:          *wire.Account.ID,
			DisplayName: *wire.Account.DisplayName,
		},
		Summary: *wire.Summary,
		Issues:  issues,
	}, nil
}

func decodeStrictJSON(rawJSON []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(rawJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}

	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected extra JSON content")
		}
		return err
	}

	return nil
}
